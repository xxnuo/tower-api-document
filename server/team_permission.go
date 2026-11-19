package main

import (
	"context"
	"encoding/json"
	"net/url"
	"path"
	"strings"
)

const renameDenied = "修改团队名称需要目标团队的超级管理员（owner）权限"

func ownsTeam(ctx context.Context, cfg Config, token, id string) bool {
	var result struct {
		Data struct{ Attributes struct{ Role string } }
	}
	status, b, err := doTowerHTTP(ctx, cfg, token, "GET", "/api/v1/teams/"+url.PathEscape(id)+"/member", "", nil)
	return err == nil && status == 200 && json.Unmarshal(b, &result) == nil && result.Data.Attributes.Role == "owner"
}

func teamWriteDenied(ctx context.Context, cfg Config, token, method, target string) bool {
	if method != "PATCH" && method != "PUT" {
		return false
	}
	u, err := url.ParseRequestURI(target)
	if err != nil {
		return true
	}
	parts := strings.Split(strings.Trim(path.Clean(u.Path), "/"), "/")
	return len(parts) == 4 && strings.Join(parts[:3], "/") == "api/v1/teams" && !ownsTeam(ctx, cfg, token, parts[3])
}

func ownsAnyTeam(ctx context.Context, cfg Config, token string) bool {
	var result struct{ Data []struct{ ID string } }
	status, b, err := doTowerHTTP(ctx, cfg, token, "GET", "/api/v1/teams", "", nil)
	if err != nil || status != 200 || json.Unmarshal(b, &result) != nil {
		return false
	}
	for _, team := range result.Data {
		if ownsTeam(ctx, cfg, token, team.ID) {
			return true
		}
	}
	return false
}
