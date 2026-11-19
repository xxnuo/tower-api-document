package main

import (
	"database/sql"
	"encoding/json"
	"time"
)

const (
	authStateOAuth  = "oauth_state"
	authStateResult = "oauth_result"
	authStateGrant  = "web_grant"
	authStateWechat = "wechat"
)

func (s *Store) putAuthState(kind, id string, owner int64, payload any, ttl time.Duration) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	now := time.Now()
	if _, err := s.db.Exec("DELETE FROM auth_states WHERE expires_at <= ? AND leased_until <= ?", now.UnixMilli(), now.UnixNano()); err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO auth_states (kind, id, owner, payload, expires_at) VALUES (?, ?, ?, ?, ?)`,
		kind, id, owner, raw, now.Add(ttl).UnixMilli())
	return err
}

func (s *Store) takeAuthState(kind, id string, owner int64, payload any) error {
	var raw []byte
	err := s.db.QueryRow(`DELETE FROM auth_states WHERE kind = ? AND id = ? AND owner = ? AND expires_at > ? RETURNING payload`,
		kind, id, owner, time.Now().UnixMilli()).Scan(&raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, payload)
}

func (s *Store) loginBlocked(key string) (bool, error) {
	var blocked bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM login_failures WHERE bucket = ? AND reset_at > ? AND count >= ?)`,
		key, time.Now().UnixMilli(), loginRateLimitMax).Scan(&blocked)
	return blocked, err
}

func (s *Store) recordLoginFailure(key string) error {
	now := time.Now()
	if _, err := s.db.Exec("DELETE FROM login_failures WHERE reset_at <= ?", now.UnixMilli()); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO login_failures (bucket, count, reset_at) VALUES (?, 1, ?)
		ON CONFLICT(bucket) DO UPDATE SET
		count = CASE WHEN reset_at <= ? THEN 1 ELSE count + 1 END,
		reset_at = CASE WHEN reset_at <= ? THEN excluded.reset_at ELSE reset_at END`,
		key, now.Add(loginRateLimitWindow).UnixMilli(), now.UnixMilli(), now.UnixMilli())
	return err
}

func (s *Store) clearLoginFailures(key string) error {
	_, err := s.db.Exec("DELETE FROM login_failures WHERE bucket = ?", key)
	return err
}

func (s *Store) claimWechat(id string, owner int64, lease int64) ([]byte, error) {
	var raw []byte
	now := time.Now().UnixMilli()
	err := s.db.QueryRow(`UPDATE auth_states SET leased_until = ?
		WHERE kind = ? AND id = ? AND owner = ? AND expires_at > ? AND leased_until <= ? RETURNING payload`,
		lease, authStateWechat, id, owner, now, time.Now().UnixNano()).Scan(&raw)
	return raw, err
}

func (s *Store) wechatInProgress(id string, owner int64) (bool, error) {
	var active bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM auth_states WHERE kind = ? AND id = ? AND owner = ? AND expires_at > ?)`,
		authStateWechat, id, owner, time.Now().UnixMilli()).Scan(&active)
	return active, err
}

func (s *Store) updateWechat(id string, owner, lease, nextLease int64, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`UPDATE auth_states SET leased_until = ?, payload = ?
		WHERE kind = ? AND id = ? AND owner = ? AND leased_until = ?`,
		nextLease, raw, authStateWechat, id, owner, lease)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (s *Store) deleteWechat(id string, owner, lease int64) error {
	_, err := s.db.Exec(`DELETE FROM auth_states WHERE kind = ? AND id = ? AND owner = ? AND leased_until = ?`,
		authStateWechat, id, owner, lease)
	return err
}
