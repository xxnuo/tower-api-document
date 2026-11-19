package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTestAuthStores(t *testing.T) (*Store, *Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shared.db")
	open := func() *Store {
		t.Helper()
		store, err := OpenStore(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		return store
	}
	return open(), open()
}

func startTestOAuth(t *testing.T, mux http.Handler, sess *http.Cookie) *http.Cookie {
	t.Helper()
	login := httptest.NewRequest(http.MethodGet, "/login", nil)
	login.AddCookie(sess)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, login)
	for _, cookie := range rec.Result().Cookies() {
		if rec.Code == http.StatusFound && cookie.Name == stateCookieName {
			return cookie
		}
	}
	t.Fatalf("expected OAuth state cookie, got %d %s", rec.Code, rec.Body.String())
	return nil
}

func TestOAuthStateAndResultAcrossInstances(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_, _ = w.Write([]byte(`{"access_token":"cross-instance"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	a, b := openTestAuthStores(t)
	cfg := testConfig(upstream.URL)
	cfg.Store = a
	blue := newMux(cfg)
	cfg.Store = b
	green := newMux(cfg)

	sess, _, _ := registerTestUser(t, blue, "blue-green", "fixture-password")
	state := startTestOAuth(t, blue, sess)
	send := func(mux http.Handler, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(sess)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	callback := "/oauth2/callback?code=fixture&state=" + state.Value
	rec := send(green, callback, state)
	if rec.Code != http.StatusFound {
		t.Fatalf("callback on green: %d %s", rec.Code, rec.Body.String())
	}
	var result *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == resultCookieName {
			result = cookie
		}
	}
	if result == nil {
		t.Fatal("missing result cookie")
	}
	if rec := send(blue, callback+"&format=json", state); rec.Code != http.StatusBadRequest {
		t.Fatalf("OAuth state was reusable: %d", rec.Code)
	}

	results := make(chan int, 2)
	for _, mux := range []http.Handler{blue, green} {
		go func() {
			rec := send(mux, "/api/auth/result", result)
			if rec.Code == http.StatusOK && !strings.Contains(rec.Body.String(), "cross-instance") {
				t.Errorf("unexpected OAuth result: %s", rec.Body.String())
			}
			results <- rec.Code
		}()
	}
	first, second := <-results, <-results
	if !(first == http.StatusOK && second == http.StatusNoContent) && !(second == http.StatusOK && first == http.StatusNoContent) {
		t.Fatalf("pending result consumed across instances: %d, %d", first, second)
	}
}

func TestLoginFailuresAcrossInstances(t *testing.T) {
	a, b := openTestAuthStores(t)
	blue, green := newMux(Config{Store: a}), newMux(Config{Store: b})
	registerTestUser(t, blue, "lockout", "fixture-password")
	login := func(mux http.Handler) int {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"lockout","password":"wrong"}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := 0; i < loginRateLimitMax; i++ {
		if code := login([]http.Handler{blue, green}[i%2]); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	if code := login(green); code != http.StatusTooManyRequests {
		t.Fatalf("expected shared lockout: %d", code)
	}
	if _, err := a.db.Exec("UPDATE login_failures SET reset_at = 0"); err != nil {
		t.Fatal(err)
	}
	if code := login(blue); code != http.StatusUnauthorized {
		t.Fatalf("expected expired lockout: %d", code)
	}
}

func TestWechatPollingStateAcrossInstances(t *testing.T) {
	a, b := openTestAuthStores(t)
	const id = "qr"
	const owner = 7
	pending := wechatPending{UUID: "qr-uuid"}
	if err := a.putAuthState(authStateWechat, id, owner, pending, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := b.claimWechat(id, owner+1, time.Now().Add(time.Minute).UnixNano()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("accepted different owner: %v", err)
	}

	var wg sync.WaitGroup
	claims := make(chan int64, 2)
	for _, store := range []*Store{a, b} {
		wg.Go(func() {
			lease := time.Now().Add(time.Minute).UnixNano()
			_, err := store.claimWechat(id, owner, lease)
			if err == nil {
				claims <- lease
			} else if !errors.Is(err, sql.ErrNoRows) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	close(claims)
	lease, ok := <-claims
	if !ok || len(claims) != 0 {
		t.Fatal("expected one poll claim across instances")
	}
	pending.Last = "404"
	if err := a.updateWechat(id, owner, lease, 0, pending); err != nil {
		t.Fatal(err)
	}
	next := time.Now().Add(time.Minute).UnixNano()
	raw, err := b.claimWechat(id, owner, next)
	if err != nil {
		t.Fatal(err)
	}
	pending = wechatPending{}
	if err := json.Unmarshal(raw, &pending); err != nil || pending.Last != "404" {
		t.Fatalf("scanner progress missing: %+v, %v", pending, err)
	}
	pending.Result = map[string]any{"status": "connected"}
	if err := b.updateWechat(id, owner, next, 0, pending); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), authUserCtxKey{}, &User{ID: owner}))
	req.SetPathValue("id", id)
	for _, store := range []*Store{a, b} {
		rec := httptest.NewRecorder()
		handleWebWechatPoll(Config{Store: store})(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "connected") {
			t.Fatalf("final result missing: %d %s", rec.Code, rec.Body.String())
		}
	}
	if _, err := a.db.Exec("UPDATE auth_states SET expires_at = 0 WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	if active, err := b.wechatInProgress(id, owner); err != nil || active {
		t.Fatalf("expired scanner remained active: %v, %v", active, err)
	}
}
