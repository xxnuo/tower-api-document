package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyTaskHistoryUsesSelectedWebAccount(t *testing.T) {
	const todo = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const project = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/todos/" + todo:
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Error("missing account bearer token")
			}
			w.Write([]byte(`{"data":{"relationships":{"project":{"data":{"id":"` + project + `"}}}}}`))
		case "/projects/" + project + "/todos/" + todo:
			c, err := r.Cookie("session")
			if err != nil || c.Value != "fixture" {
				t.Error("missing web session")
			}
			w.Write([]byte(`<tr-comments><div class="event" id="event-1"><span class="event-action">变更截止日期</span></div></tr-comments>`))
		default:
			t.Error(r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	cfg := testConfig(upstream.URL)
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg.Store = store
	mux := newMux(cfg)
	ownerCookie, user, _ := registerTestUser(t, mux, "owner", "password123")
	guestCookie, _, _ := registerTestUser(t, mux, "guest", "password123")
	acct, err := store.SaveLoginForUser(user.ID, &UserInfo{ID: "abc"}, TokenResponse{AccessToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveWebSession(acct.ID, []webCookie{{Name: "session", Value: "fixture", Path: "/"}}, "", ""); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cookie        *http.Cookie
		method, query string
		status        int
	}{
		{ownerCookie, "GET", "offset=0&page_size=1", 200}, {ownerCookie, "GET", "page_size=-1", 400}, {ownerCookie, "DELETE", "", 405}, {guestCookie, "GET", "", 404},
	} {
		data, _ := json.Marshal(map[string]any{"method": tc.method, "path": "/api/v1/todos/" + todo + "/history", "query": tc.query, "access_token": acct.MCPAPIKey})
		r := httptest.NewRequest("POST", "/api/proxy", strings.NewReader(string(data)))
		r.AddCookie(tc.cookie)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var response struct {
			Status int
			Body   json.RawMessage
		}
		if json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Status != tc.status || (tc.status == 200 && !strings.Contains(string(response.Body), "变更截止日期")) {
			t.Errorf("%s %s: %d, %s", tc.method, tc.query, w.Code, w.Body)
		}
	}
}
