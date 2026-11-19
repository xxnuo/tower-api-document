package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebLibrary(t *testing.T) {
	const team = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const library = "/teams/42/repositories/80"
	const folder = "/teams/42/repository_folders/21"
	const markdown = "/teams/42/repository_documents/22"
	const office = "/teams/42/repository_documents/23"
	const upload = "/teams/42/repository_uploads/24"
	const wrongTeam = "/teams/43/repository_documents/22"
	const header = `<input type="hidden" id="team-guid" value="` + team + `">`
	const root = `<a class="breadcrumb-name" href="` + library + `">Library</a>`
	requests := map[string]int{}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		requests[p]++
		if r.Method != http.MethodGet || r.URL.RawQuery != "" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		var body string
		switch p {
		case "/teams/" + team + "/projects":
			body = `<li id="nav-repository"><a href="/teams/42/repositories">Knowledge base</a></li>`
		case "/teams/42/repositories":
			http.Redirect(w, r, library, http.StatusFound)
			return
		case library:
			body = `<tr-repository-page><h2 class="header-title">Library</h2><tr-repository-folder-table><a class="item-name" href="` + folder + `">Directory</a><a class="item-name" href="` + markdown + `">Markdown</a><a class="item-name" href="` + wrongTeam + `">Other team</a><a class="item-name" href="` + upload + `?signature=hidden">Tokenized path</a></tr-repository-folder-table></tr-repository-page>`
		case folder:
			body = `<tr-repository-page>` + root + `<h1 class="breadcrumb-name">Directory</h1><tr-repository-folder-table><a class="item-name" href="` + upload + `">File</a></tr-repository-folder-table></tr-repository-page>`
		case markdown:
			body = `<tr-repository-page>` + root + `<h1 class="document-title">Markdown</h1><div class="document-content"><tr-markdown-parser content="  # Title&#10;&#10;  &lt;a&gt; &amp; text&#10;"></tr-markdown-parser></div></tr-repository-page>`
		case office:
			body = `<tr-repository-page>` + root + `<h1 class="document-title">Office</h1><a href="https://files.invalid/?signature=hidden">download</a></tr-repository-page>`
		case upload:
			body = `<tr-repository-page>` + root + `<h1 class="upload-title">File</h1></tr-repository-page>`
		case "/teams/42/repository_documents/25":
			http.Redirect(w, r, wrongTeam, http.StatusFound)
			return
		default:
			t.Errorf("unexpected path %s", p)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(header + body))
	}))
	defer origin.Close()

	web, err := newWebClient(origin.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	check := func(tool, path string) map[string]any {
		t.Helper()
		got, err := web.library(context.Background(), tool, team, path)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := check("list_repositories", "")
	if items := got["items"].([]libraryItem); len(items) != 1 || items[0].URL != origin.URL+library {
		t.Fatalf("repositories = %+v", got)
	}
	got = check("list_repository_items", library)
	if items := got["items"].([]libraryItem); len(items) != 2 || items[0].Type != "folder" {
		t.Fatalf("root items = %+v", got)
	}
	got = check("list_repository_items", origin.URL+folder+"/")
	if items := got["items"].([]libraryItem); len(items) != 1 || items[0].Type != "upload" || got["repository_url"] != origin.URL+library {
		t.Fatalf("folder items = %+v", got)
	}
	got = check("read_repository_item", markdown)
	if got["markdown"] != "  # Title\n\n  <a> & text\n" {
		t.Fatalf("markdown = %q", got["markdown"])
	}
	for _, p := range []string{office, upload} {
		got = check("read_repository_item", p)
		if got["format"] != "link" || got["url"] != origin.URL+p || got["markdown"] != nil || strings.Contains(renderAgentValue(got), "signature") {
			t.Fatalf("fallback = %+v", got)
		}
	}
	for _, p := range []string{wrongTeam, "//wrong.invalid" + markdown, origin.URL + markdown + "?token=secret", strings.Replace(markdown, "/22", "/%32%32", 1), library, "/teams/42/repository_documents/25"} {
		if _, err := web.library(context.Background(), "read_repository_item", team, p); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe path %q accepted or exposed: %v", p, err)
		}
	}
	if requests[wrongTeam] != 0 {
		t.Fatal("fetched a resource for another team")
	}
}
