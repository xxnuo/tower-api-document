package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestWebGrantConsentAndOTP(t *testing.T) {
	const callback = "https://gateway.example/oauth2/callback"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/account_sign_in", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<meta name="csrf-token" content="test">`)
	})
	mux.HandleFunc("POST /users/account_sign_in", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "rebound", Path: "/"})
		fmt.Fprint(w, `{"success":true}`)
	})
	mux.HandleFunc("GET /launchpad/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<input id="user-email" value="owner@example.com">`)
	})
	mux.HandleFunc("GET /oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<form method="post" action="/oauth/authorize"><input type="hidden" name="client_id" value="app"><input type="hidden" name="redirect_uri" value="%s"><input type="hidden" name="state" value="%s"></form>`, callback, r.URL.Query().Get("state"))
	})
	mux.HandleFunc("POST /oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("session"); err != nil || (cookie.Value != "test" && cookie.Value != "rebound") {
			http.Error(w, "missing restored session", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, callback+"?code=granted&state="+url.QueryEscape(r.FormValue("state")), 302)
	})
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("code") != "granted" {
			http.Error(w, "invalid code", 400)
			return
		}
		if r.FormValue("captcha") != "123456" {
			fmt.Fprint(w, `{"error":"otp_required"}`)
			return
		}
		fmt.Fprint(w, `{"access_token":"test-token"}`)
	})
	mux.HandleFunc("GET /api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"id":"user1","attributes":{"email":"owner@example.com"}}}`)
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()
	db, standby := openTestAuthStores(t)
	cfg := Config{Store: db, ClientID: "app", BaseURL: upstream.URL, RedirectURI: callback, HTTPClient: upstream.Client()}
	client, err := newWebClient(cfg.BaseURL, []webCookie{{Name: "session", Value: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	client.email = "owner@example.com"
	result, err := finishWebLogin(context.Background(), cfg, 12, "", client, false)
	if err != nil || result["status"] != "consent" {
		t.Fatal(result, err)
	}
	send := func(store *Store, owner int64, ticket any, extra string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"challenge":"%s",%s}`, ticket, extra)))
		req = req.WithContext(context.WithValue(req.Context(), authUserCtxKey{}, &User{ID: owner}))
		rec := httptest.NewRecorder()
		instance := cfg
		instance.Store = store
		handleGrantContinue(instance)(rec, req)
		return rec
	}
	if rec := send(standby, 13, result["challenge"], `"approve":true`); rec.Code != 409 {
		t.Fatal(rec.Code)
	}
	rec := send(standby, 12, result["challenge"], `"approve":true`)
	var otp struct{ Status, Challenge string }
	if err := json.Unmarshal(rec.Body.Bytes(), &otp); err != nil || otp.Status != "otp" {
		t.Fatal(rec.Body.String())
	}
	if rec := send(db, 12, otp.Challenge, `"captcha":"123456"`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if _, err := db.GetWebSession("u12:user1"); err != nil {
		t.Fatal(err)
	}
	if rec := send(standby, 12, otp.Challenge, `"captcha":"123456"`); rec.Code != 409 {
		t.Fatal("reused challenge")
	}
	client.email = "other@example.com"
	if _, err := (&webGrant{owner: 14, client: client, code: "granted"}).exchange(cfg, "123456"); err == nil {
		t.Fatal("mismatched user")
	}
	if _, err := db.GetByID("u14:user1"); err == nil {
		t.Fatal("unexpected account")
	}

	for _, towerID := range []string{"user2", "user1"} {
		t.Run("rebind_"+towerID, func(t *testing.T) {
			before, err := db.SaveLoginForUser(15, &UserInfo{ID: towerID, Email: "owner@example.com"}, TokenResponse{AccessToken: "old-" + towerID})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"account_id":%q,"rebind":true,"email":"owner@example.com","password":"fixture"}`, before.ID)))
			req = req.WithContext(context.WithValue(req.Context(), authUserCtxKey{}, &User{ID: 15}))
			rec := httptest.NewRecorder()
			handleWebEmailLogin(cfg)(rec, req)
			var reply struct{ Status, Challenge string }
			if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || rec.Code != 200 || reply.Status != "consent" {
				t.Fatalf("rebind did not start OAuth: %s", rec.Body.String())
			}
			rec = send(standby, 15, reply.Challenge, `"approve":true`)
			if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || rec.Code != 200 || reply.Status != "otp" {
				t.Fatalf("missing OTP challenge: %s", rec.Body.String())
			}
			rec = send(db, 15, reply.Challenge, `"captcha":"123456"`)
			after, err := db.GetByID(before.ID)
			if err != nil {
				t.Fatal(err)
			}
			if towerID == "user2" {
				if rec.Code != 502 || after.AccessToken != before.AccessToken {
					t.Fatalf("rebind accepted a different Tower ID: %s", rec.Body.String())
				}
				if _, err := db.GetByID("u15:user1"); err == nil {
					t.Fatal("rebind created a different binding")
				}
				return
			}
			if rec.Code != 200 || after.AccessToken != "test-token" || after.MCPAPIKey != before.MCPAPIKey {
				t.Fatalf("rebind failed to refresh the token and preserve the MCP key: %s", rec.Body.String())
			}
			if session, err := db.GetWebSession(before.ID); err != nil || len(session.Cookies) != 1 || session.Cookies[0].Value != "rebound" {
				t.Fatalf("web session was not refreshed: %v", err)
			}
		})
	}
}
