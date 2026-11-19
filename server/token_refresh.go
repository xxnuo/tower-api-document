package main

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	tokenRefreshTimeout = 15 * time.Second
	tokenRefreshLease   = 30 * time.Second
)

func (s *Store) EnsureFreshToken(ctx context.Context, cfg Config, observed *StoredAccount, failedAccessToken string) (*StoredAccount, error) {
	if failedAccessToken != "" && failedAccessToken != observed.AccessToken {
		return observed, nil
	}
	if failedAccessToken == "" && !observed.NeedsRefresh(time.Now()) {
		return observed, nil
	}
	if failedAccessToken != "" && observed.RefreshToken == "" {
		return nil, errors.New("Tower Access Token 已失效且无可用 Refresh Token，请重新登录 AgentsTower")
	}
	token, err := s.refreshAccount(ctx, cfg, observed, failedAccessToken, observed.RefreshToken, observed.AccessToken)
	if err == nil && token.Error != "" {
		err = errors.New(formatOAuthError(token.Error, token.ErrorDescription))
	}
	if err != nil {
		return nil, errors.New("自动刷新 Tower Token 失败: " + err.Error())
	}
	return s.GetByID(observed.ID)
}

func (s *Store) refreshAccount(ctx context.Context, cfg Config, observed *StoredAccount, failedAccessToken, refreshToken, bearer string) (TokenResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, tokenRefreshLease+tokenRefreshTimeout)
	defer cancel()
	owner, err := generateSessionToken()
	if err != nil {
		return TokenResponse{}, err
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		acc, err := scanAccount(s.db.QueryRowContext(ctx, "SELECT "+selectAccountCols+" FROM accounts WHERE id = ?", observed.ID))
		if err != nil {
			return TokenResponse{}, err
		}
		if acc.AccessToken != observed.AccessToken || acc.RefreshToken != observed.RefreshToken ||
			acc.TokenCreatedAt != observed.TokenCreatedAt || acc.ExpiresIn != observed.ExpiresIn ||
			(failedAccessToken != "" && failedAccessToken != acc.AccessToken) {
			return acc.ToLoginResult().Token, nil
		}

		// Match the observed credentials when acquiring the lease so a concurrent
		// update between the read and the acquisition cannot reuse a spent token.
		now := time.Now()
		res, err := s.db.ExecContext(ctx, `
			INSERT INTO token_refresh_leases (account_id, owner, expires_at)
			SELECT id, ?, ? FROM accounts WHERE id = ? AND access_token = ? AND refresh_token = ? AND token_created_at = ? AND expires_in = ?
			ON CONFLICT(account_id) DO UPDATE SET owner = excluded.owner, expires_at = excluded.expires_at
			WHERE token_refresh_leases.expires_at <= ?`,
			owner, now.Add(tokenRefreshLease).UnixMilli(), acc.ID, acc.AccessToken, acc.RefreshToken, acc.TokenCreatedAt, acc.ExpiresIn, now.UnixMilli())
		if err != nil {
			return TokenResponse{}, err
		}
		acquired, err := res.RowsAffected()
		if err != nil {
			return TokenResponse{}, err
		}
		if acquired == 1 {
			defer func() {
				releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = s.db.ExecContext(releaseCtx, `DELETE FROM token_refresh_leases WHERE account_id = ? AND owner = ?`, acc.ID, owner)
			}()

			refreshCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), now.Add(tokenRefreshTimeout))
			token, err := postTokenContext(refreshCtx, cfg, refreshTokenForm(cfg, refreshToken), bearer)
			cancel()
			if err != nil || token.Error != "" {
				return token, err
			}
			if token.AccessToken == "" {
				return token, errors.New("Tower 未返回 Access Token")
			}
			createdAt := token.CreatedAt
			if createdAt <= 0 {
				createdAt = time.Now().Unix()
			}
			nextRefresh := strings.TrimSpace(token.RefreshToken)
			if nextRefresh == "" {
				nextRefresh = acc.RefreshToken
			}
			tokenType := strings.TrimSpace(token.TokenType)
			if tokenType == "" {
				tokenType = acc.TokenType
			}
			expiresIn := token.ExpiresIn
			if expiresIn <= 0 {
				expiresIn = acc.ExpiresIn
			}
			// Persist a completed rotation even if the initiating client disconnected.
			saveCtx, cancel := context.WithDeadline(context.Background(), now.Add(tokenRefreshLease))
			defer cancel()
			res, err := s.db.ExecContext(saveCtx, `
				UPDATE accounts SET access_token = ?, refresh_token = ?, token_type = ?, expires_in = ?, token_created_at = ?, updated_at = ?
				WHERE id = ? AND access_token = ? AND refresh_token = ? AND token_created_at = ? AND expires_in = ?
				AND EXISTS (SELECT 1 FROM token_refresh_leases WHERE account_id = accounts.id AND owner = ?)`,
				token.AccessToken, nextRefresh, tokenType, expiresIn, createdAt, time.Now().Unix(),
				acc.ID, acc.AccessToken, acc.RefreshToken, acc.TokenCreatedAt, acc.ExpiresIn, owner)
			if err != nil {
				return token, err
			}
			updated, err := res.RowsAffected()
			if err != nil || updated == 1 {
				return token, err
			}
			current, err := s.GetByID(acc.ID)
			if err != nil {
				return TokenResponse{}, err
			}
			return current.ToLoginResult().Token, nil
		}
		select {
		case <-ctx.Done():
			return TokenResponse{}, ctx.Err()
		case <-ticker.C:
		}
	}
}
