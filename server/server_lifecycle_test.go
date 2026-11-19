package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGracefulShutdownDrainsActiveRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	releaseRequest := sync.OnceFunc(func() { close(release) })
	defer releaseRequest()
	shuttingDown := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		close(started)
		select {
		case <-release:
			_, _ = io.WriteString(w, "drained")
		case <-req.Context().Done():
		}
	})}
	server.RegisterOnShutdown(func() { close(shuttingDown) })
	done := make(chan error, 1)
	go func() { done <- serveUntilShutdown(ctx, server, listener) }()
	result := make(chan error, 1)
	client := &http.Client{Timeout: 5 * time.Second}
	go func() {
		resp, err := client.Get("http://" + listener.Addr().String())
		if err != nil {
			result <- err
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err == nil && string(body) != "drained" {
			err = fmt.Errorf("response body = %q", body)
		}
		result <- err
	}()
	select {
	case <-started:
	case err := <-result:
		t.Fatalf("request ended before entering the handler: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case <-shuttingDown:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not start")
	}
	select {
	case err := <-done:
		t.Fatalf("shutdown returned with a request in flight: %v", err)
	default:
	}
	releaseRequest()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish")
	}
}

func TestReadinessChecksStore(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cfg := testConfig("")
	cfg.Store = store
	handler := newMux(cfg)
	check := func(want int) {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/readyz", nil))
		if rec.Code != want {
			t.Fatalf("readiness = %d, want %d: %s", rec.Code, want, rec.Body)
		}
	}
	check(http.StatusOK)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	check(http.StatusServiceUnavailable)
}

func TestTokenRefreshCoordinatesAcrossStores(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%t", force), func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "data.db")
			first, err := OpenStore(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			second, err := OpenStore(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			const oldAccess = "old-access"
			const oldRefresh = "old-refresh"
			createdAt := time.Now().Add(-2 * time.Hour).Unix()
			if force {
				createdAt = time.Now().Unix()
			}
			acc, err := first.SaveLogin(&UserInfo{ID: "tower"}, TokenResponse{
				AccessToken: oldAccess, RefreshToken: oldRefresh, ExpiresIn: 3600, CreatedAt: createdAt,
			})
			if err != nil {
				t.Fatal(err)
			}
			other, err := second.GetByID(acc.ID)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			entered := make(chan struct{})
			release := make(chan struct{})
			releaseRefresh := sync.OnceFunc(func() { close(release) })
			tower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/api/v1/user" {
					if req.Header.Get("Authorization") != "Bearer new-access" {
						w.WriteHeader(http.StatusUnauthorized)
					}
					return
				}
				if calls.Add(1) != 1 || req.FormValue("refresh_token") != oldRefresh {
					_ = json.NewEncoder(w).Encode(TokenResponse{Error: "invalid_grant"})
					return
				}
				close(entered)
				<-release
				_ = json.NewEncoder(w).Encode(TokenResponse{AccessToken: "new-access", RefreshToken: "new-refresh"})
			}))
			defer tower.Close()
			defer releaseRefresh()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			results := make(chan error, 2)
			refresh := func(store *Store, observed *StoredAccount) {
				cfg := Config{BaseURL: tower.URL, HTTPClient: tower.Client(), Store: store}
				var err error
				var fresh *StoredAccount
				if force {
					var status int
					status, _, err = doTowerHTTPWithRefresh(ctx, cfg, observed.MCPAPIKey, observed, http.MethodGet, "/api/v1/user", "", nil)
					if err == nil && status != http.StatusOK {
						err = fmt.Errorf("Tower retry status = %d", status)
					}
					if err == nil {
						fresh, err = store.GetByID(observed.ID)
					}
				} else {
					fresh, err = store.EnsureFreshToken(ctx, cfg, observed, "")
				}
				if err == nil && (fresh.AccessToken != "new-access" || fresh.RefreshToken != "new-refresh") {
					err = fmt.Errorf("stored token pair was not updated")
				}
				results <- err
			}
			go refresh(first, acc)
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("refresh did not start")
			}
			go refresh(second, other)
			select {
			case err := <-results:
				t.Fatalf("refresh returned while the upstream request was blocked: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			releaseRefresh()
			for range 2 {
				select {
				case err := <-results:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("refresh did not finish")
				}
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("Tower refresh calls = %d", got)
			}
			if force {
				// A delayed 401 must still use the new token.
				refresh(second, other)
				if err := <-results; err != nil {
					t.Fatal(err)
				}
				if got := calls.Load(); got != 1 {
					t.Fatalf("Tower refresh calls after delayed 401 = %d", got)
				}
			}
		})
	}
}
