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
	mux.HandleFunc("GET /oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<form method="post" action="/oauth/authorize"><input type="hidden" name="client_id" value="app"><input type="hidden" name="redirect_uri" value="%s"><input type="hidden" name="state" value="%s"></form>`, callback, r.URL.Query().Get("state"))
	})
	mux.HandleFunc("POST /oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
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
	db, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := Config{Store: db, ClientID: "app", BaseURL: upstream.URL, RedirectURI: callback, HTTPClient: upstream.Client()}
	client, err := newWebClient(cfg.BaseURL, []webCookie{{Name: "session", Value: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	client.email = "owner@example.com"
	result, err := finishWebLogin(context.Background(), cfg, 12, "", client)
	if err != nil || result["status"] != "consent" {
		t.Fatal(result, err)
	}
	send := func(owner int64, ticket any, extra string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"challenge":"%s",%s}`, ticket, extra)))
		req = req.WithContext(context.WithValue(req.Context(), authUserCtxKey{}, &User{ID: owner}))
		rec := httptest.NewRecorder()
		handleGrantContinue(cfg)(rec, req)
		return rec
	}
	if rec := send(13, result["challenge"], `"approve":true`); rec.Code != 409 {
		t.Fatal(rec.Code)
	}
	rec := send(12, result["challenge"], `"approve":true`)
	var otp struct{ Status, Challenge string }
	if err := json.Unmarshal(rec.Body.Bytes(), &otp); err != nil || otp.Status != "otp" {
		t.Fatal(rec.Body.String())
	}
	if rec := send(12, otp.Challenge, `"captcha":"123456"`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if _, err := db.GetWebSession("u12:user1"); err != nil {
		t.Fatal(err)
	}
	if rec := send(12, otp.Challenge, `"captcha":"123456"`); rec.Code != 409 {
		t.Fatal("reused challenge")
	}
	client.email = "other@example.com"
	if _, err := (&webGrant{owner: 14, client: client, code: "granted"}).exchange(cfg, "123456"); err == nil {
		t.Fatal("mismatched user")
	}
	if _, err := db.GetByID("u14:user1"); err == nil {
		t.Fatal("unexpected account")
	}
}
