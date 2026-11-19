package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrAccountNotFound = errors.New("account not found")
	ErrUserNotFound    = errors.New("user not found")
	ErrUserExists      = errors.New("username already exists")
	ErrInvalidCreds    = errors.New("invalid username or password")
	ErrSessionInvalid  = errors.New("session invalid or expired")
	ErrLastAdmin       = errors.New("cannot revoke the last admin")
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"

	pbkdf2Iterations = 100_000
	pbkdf2KeyLen     = 32
	sessionTTL       = 30 * 24 * time.Hour
)

type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

type UserSummary struct {
	User
	TowerCount  int    `json:"tower_count"`
	TowerEmails string `json:"tower_emails,omitempty"`
}

type StoredAccount struct {
	ID             string
	UserID         int64
	TowerID        string
	Nickname       string
	Email          string
	Avatar         string
	AccessToken    string
	RefreshToken   string
	TokenType      string
	ExpiresIn      int
	TokenCreatedAt int64
	MCPAPIKey      string
	CreatedAt      int64
	UpdatedAt      int64
}

func (acc *StoredAccount) ToLoginResult() LoginResult {
	towerID := acc.TowerID
	if towerID == "" {
		towerID = acc.ID
	}
	var user *UserInfo
	if towerID != "" || acc.Nickname != "" || acc.Email != "" || acc.Avatar != "" {
		user = &UserInfo{
			ID:       towerID,
			Nickname: acc.Nickname,
			Email:    acc.Email,
			Avatar:   acc.Avatar,
		}
	}
	return LoginResult{
		AccountID: acc.ID,
		User:      user,
		Token: TokenResponse{
			AccessToken:  acc.AccessToken,
			TokenType:    acc.TokenType,
			ExpiresIn:    acc.ExpiresIn,
			RefreshToken: acc.RefreshToken,
			CreatedAt:    acc.TokenCreatedAt,
			Email:        acc.Email,
		},
		MCPAPIKey: acc.MCPAPIKey,
	}
}

func (acc *StoredAccount) NeedsRefresh(now time.Time) bool {
	if acc.RefreshToken == "" || acc.TokenCreatedAt <= 0 || acc.ExpiresIn <= 0 {
		return false
	}
	expiresAt := time.Unix(acc.TokenCreatedAt+int64(acc.ExpiresIn), 0)
	return !now.Add(60 * time.Second).Before(expiresAt)
}

type Store struct {
	db        *sql.DB
	refreshMu sync.Mutex
}

const defaultDBPath = "data/data.db"

func OpenStore(dbPath string) (*Store, error) {
	if dbPath == "" {
		dbPath = defaultDBPath
	}
	if dbPath != ":memory:" && !strings.HasPrefix(dbPath, "file:") {
		if filepath.Base(dbPath) == "data.db" {
			// Keep opening the legacy SQLite database in place, including its WAL.
			if _, err := os.Lstat(dbPath); errors.Is(err, os.ErrNotExist) {
				legacyPath := filepath.Join(filepath.Dir(dbPath), "agents-tower.db")
				if _, err := os.Lstat(legacyPath); err == nil {
					log.Printf("using existing SQLite database %s", legacyPath)
					dbPath = legacyPath
				} else if !errors.Is(err, os.ErrNotExist) {
					return nil, err
				}
			} else if err != nil {
				return nil, err
			}
		}
		if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
		}
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	schema := `
	PRAGMA journal_mode = WAL;
	PRAGMA busy_timeout = 5000;
	PRAGMA foreign_keys = ON;
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE COLLATE NOCASE,
		display_name TEXT NOT NULL DEFAULT '',
		password_hash TEXT NOT NULL,
		role TEXT NOT NULL DEFAULT 'user',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS sessions (
		token TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
	CREATE TABLE IF NOT EXISTS accounts (
		id TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL DEFAULT 0,
		tower_id TEXT NOT NULL DEFAULT '',
		nickname TEXT NOT NULL DEFAULT '',
		email TEXT NOT NULL DEFAULT '',
		avatar TEXT NOT NULL DEFAULT '',
		access_token TEXT NOT NULL,
		refresh_token TEXT NOT NULL DEFAULT '',
		token_type TEXT NOT NULL DEFAULT 'bearer',
		expires_in INTEGER NOT NULL DEFAULT 0,
		token_created_at INTEGER NOT NULL DEFAULT 0,
		mcp_api_key TEXT NOT NULL UNIQUE,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_accounts_mcp_api_key ON accounts(mcp_api_key);
	CREATE INDEX IF NOT EXISTS idx_accounts_access_token ON accounts(access_token);
	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS mcp_log_opt_ins (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE
	);
	CREATE TABLE IF NOT EXISTS mcp_operation_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		username TEXT NOT NULL,
		tower_account TEXT NOT NULL,
		tool TEXT NOT NULL,
		started_at INTEGER NOT NULL,
		duration_ms INTEGER NOT NULL,
		success BOOLEAN NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_mcp_operation_logs_user ON mcp_operation_logs(user_id, id);
	CREATE INDEX IF NOT EXISTS idx_mcp_operation_logs_expiry ON mcp_operation_logs(` + mcpLogExpirySQL + `);
	CREATE TABLE IF NOT EXISTS web_sessions (
		account_id TEXT PRIMARY KEY,
		cookies TEXT NOT NULL,
		email TEXT NOT NULL DEFAULT '',
		member_guid TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL,
		FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE
	);
	`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := migrateAccountsSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	store := &Store{db: db}
	if err := store.purgeExpiredMCPLogs(time.Now()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func migrateAccountsSchema(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(accounts)`)
	if err != nil {
		return err
	}
	hasUserID, hasTowerID := false, false
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "user_id" {
			hasUserID = true
		}
		if name == "tower_id" {
			hasTowerID = true
		}
	}
	rows.Close()

	if !hasUserID {
		if _, err := db.Exec(`ALTER TABLE accounts ADD COLUMN user_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if !hasTowerID {
		if _, err := db.Exec(`ALTER TABLE accounts ADD COLUMN tower_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
		if _, err := db.Exec(`UPDATE accounts SET tower_id = id WHERE tower_id = ''`); err != nil {
			return err
		}
	}
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_accounts_user_id ON accounts(user_id)`)
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func generateMCPKey() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "at_sk-" + hex.EncodeToString(buf), nil
}

func generateSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "at_sess-" + hex.EncodeToString(buf), nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, pbkdf2KeyLen)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(dk), nil
}

func verifyPassword(password, encoded string) bool {
	saltHex, hashHex, ok := strings.Cut(encoded, ":")
	if !ok {
		return false
	}
	salt, err1 := hex.DecodeString(saltHex)
	expected, err2 := hex.DecodeString(hashHex)
	if err1 != nil || err2 != nil || len(salt) == 0 || len(expected) == 0 {
		return false
	}
	dk, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, len(expected))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(dk, expected) == 1
}

func (s *Store) HasUsers() (bool, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func scanUser(row interface{ Scan(dest ...any) error }) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) GetUserByID(id int64) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT id, username, display_name, role, created_at, updated_at FROM users WHERE id = ?`, id))
}

func (s *Store) GetUserByUsername(username string) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT id, username, display_name, role, created_at, updated_at FROM users WHERE username = ?`, strings.TrimSpace(username)))
}

func (s *Store) CreateUser(username, password, displayName string) (*User, error) {
	uname := strings.TrimSpace(username)
	dname := strings.TrimSpace(displayName)
	if dname == "" {
		dname = uname
	}
	pwHash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var existingCount int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM users WHERE username = ?`, uname).Scan(&existingCount); err != nil {
		return nil, err
	}
	if existingCount > 0 {
		return nil, ErrUserExists
	}

	var totalUsers int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM users`).Scan(&totalUsers); err != nil {
		return nil, err
	}
	role := RoleUser
	if totalUsers == 0 {
		role = RoleAdmin
	}

	res, err := tx.Exec(
		`INSERT INTO users (username, display_name, password_hash, role, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		uname, dname, pwHash, role, now, now,
	)
	if err != nil {
		return nil, err
	}
	userID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if totalUsers == 0 {
		_, _ = tx.Exec(`UPDATE accounts SET user_id = ? WHERE user_id = 0`, userID)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetUserByID(userID)
}

func (s *Store) AuthenticateUser(username, password string) (*User, error) {
	var u User
	var pwHash string
	err := s.db.QueryRow(
		`SELECT id, username, display_name, password_hash, role, created_at, updated_at FROM users WHERE username = ?`,
		strings.TrimSpace(username),
	).Scan(&u.ID, &u.Username, &u.DisplayName, &pwHash, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidCreds
	}
	if err != nil {
		return nil, err
	}
	if !verifyPassword(password, pwHash) {
		return nil, ErrInvalidCreds
	}
	return &u, nil
}

func (s *Store) CreateSession(userID int64) (string, error) {
	now := time.Now()
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, now.Unix())
	tok, err := generateSessionToken()
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(
		`INSERT INTO sessions (token, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		tok, userID, now.Add(sessionTTL).Unix(), now.Unix(),
	)
	if err != nil {
		return "", err
	}
	return tok, nil
}

func (s *Store) GetUserBySession(sessionToken string) (*User, error) {
	tok := strings.TrimSpace(sessionToken)
	if tok == "" {
		return nil, ErrSessionInvalid
	}
	now := time.Now().Unix()
	var u User
	err := s.db.QueryRow(
		`SELECT u.id, u.username, u.display_name, u.role, u.created_at, u.updated_at
		 FROM sessions s
		 JOIN users u ON u.id = s.user_id
		 WHERE s.token = ? AND s.expires_at > ?`,
		tok, now,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionInvalid
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) DeleteSession(sessionToken string) error {
	tok := strings.TrimSpace(sessionToken)
	if tok == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, tok)
	return err
}

func (s *Store) SetUserRole(targetUserID int64, role string) (*User, error) {
	if role != RoleAdmin && role != RoleUser {
		return nil, errors.New("invalid role")
	}
	u, err := s.GetUserByID(targetUserID)
	if err != nil {
		return nil, err
	}
	if u.Role == RoleAdmin && role == RoleUser {
		var adminCount int
		if err := s.db.QueryRow(`SELECT COUNT(1) FROM users WHERE role = ?`, RoleAdmin).Scan(&adminCount); err != nil {
			return nil, err
		}
		if adminCount <= 1 {
			return nil, ErrLastAdmin
		}
	}
	_, err = s.db.Exec(`UPDATE users SET role = ?, updated_at = ? WHERE id = ?`, role, time.Now().Unix(), targetUserID)
	if err != nil {
		return nil, err
	}
	return s.GetUserByID(targetUserID)
}

func (s *Store) SetUserRoleByUsername(username, role string) (*User, error) {
	u, err := s.GetUserByUsername(username)
	if err != nil {
		return nil, err
	}
	return s.SetUserRole(u.ID, role)
}

func (s *Store) SetUserPassword(targetUserID int64, newPassword string) error {
	if _, err := s.GetUserByID(targetUserID); err != nil {
		return err
	}
	pwHash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, pwHash, time.Now().Unix(), targetUserID)
	if err != nil {
		return err
	}
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, targetUserID)
	return nil
}

func (s *Store) ChangeUserPassword(userID int64, currentPassword, newPassword string) error {
	var pwHash string
	err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&pwHash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if !verifyPassword(currentPassword, pwHash) {
		return ErrInvalidCreds
	}
	return s.SetUserPassword(userID, newPassword)
}

func (s *Store) DeleteUserSessions(userID int64) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

const (
	RegModeOpen   = "open"
	RegModeInvite = "invite"
	RegModeClosed = "closed"
)

type SystemSettings struct {
	RegistrationMode string `json:"registration_mode"`
	InviteCode       string `json:"invite_code,omitempty"`
	HasInviteCode    bool   `json:"has_invite_code"`
	AllowMCPDelete   bool   `json:"allow_mcp_delete"`
}

func (s *Store) CountTowerConnectedColleagues() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users
		WHERE EXISTS (SELECT 1 FROM accounts WHERE accounts.user_id = users.id)`).Scan(&count)
	return count, err
}

func (s *Store) GetSettings() SystemSettings {
	out := SystemSettings{
		RegistrationMode: RegModeOpen,
		InviteCode:       strings.TrimSpace(os.Getenv("INVITE_CODE")),
		AllowMCPDelete:   strings.EqualFold(strings.TrimSpace(os.Getenv("ALLOW_MCP_DELETE")), "true"),
	}
	if envMode := strings.ToLower(strings.TrimSpace(os.Getenv("REGISTRATION_MODE"))); envMode == RegModeOpen || envMode == RegModeInvite || envMode == RegModeClosed {
		out.RegistrationMode = envMode
	}
	if s == nil || s.db == nil {
		out.HasInviteCode = out.InviteCode != ""
		return out
	}
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		out.HasInviteCode = out.InviteCode != ""
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		switch k {
		case "registration_mode":
			if v == RegModeOpen || v == RegModeInvite || v == RegModeClosed {
				out.RegistrationMode = v
			}
		case "invite_code":
			out.InviteCode = strings.TrimSpace(v)
		case "allow_mcp_delete":
			out.AllowMCPDelete = strings.EqualFold(strings.TrimSpace(v), "true")
		}
	}
	out.HasInviteCode = out.InviteCode != ""
	return out
}

func (s *Store) UpdateSettings(regMode string, inviteCode *string, allowMCPDelete *bool) (SystemSettings, error) {
	cur := s.GetSettings()
	if regMode != "" {
		if regMode != RegModeOpen && regMode != RegModeInvite && regMode != RegModeClosed {
			return cur, errors.New("无效的注册模式，仅支持 open、invite 或 closed")
		}
		cur.RegistrationMode = regMode
	}
	if inviteCode != nil {
		cur.InviteCode = strings.TrimSpace(*inviteCode)
	}
	if cur.RegistrationMode == RegModeInvite && cur.InviteCode == "" {
		return cur, errors.New("开启邀请码注册模式时，邀请码不能为空")
	}
	if allowMCPDelete != nil {
		cur.AllowMCPDelete = *allowMCPDelete
	}
	upsert := func(k, v string) error {
		_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v)
		return err
	}
	if err := upsert("registration_mode", cur.RegistrationMode); err != nil {
		return cur, err
	}
	if err := upsert("invite_code", cur.InviteCode); err != nil {
		return cur, err
	}
	allowStr := "false"
	if cur.AllowMCPDelete {
		allowStr = "true"
	}
	if err := upsert("allow_mcp_delete", allowStr); err != nil {
		return cur, err
	}
	return s.GetSettings(), nil
}

func (s *Store) DeleteUser(targetUserID int64) error {
	u, err := s.GetUserByID(targetUserID)
	if err != nil {
		return err
	}
	if u.Role == RoleAdmin {
		var adminCount int
		if err := s.db.QueryRow(`SELECT COUNT(1) FROM users WHERE role = ?`, RoleAdmin).Scan(&adminCount); err != nil {
			return err
		}
		if adminCount <= 1 {
			return ErrLastAdmin
		}
	}
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, targetUserID)
	_, _ = s.db.Exec(`DELETE FROM accounts WHERE user_id = ?`, targetUserID)
	_, err = s.db.Exec(`DELETE FROM users WHERE id = ?`, targetUserID)
	return err
}

func (s *Store) ListUsers() ([]UserSummary, error) {
	rows, err := s.db.Query(`
		SELECT u.id, u.username, u.display_name, u.role, u.created_at, u.updated_at,
		       COUNT(a.id) AS tower_count,
		       COALESCE(GROUP_CONCAT(CASE WHEN a.email != '' THEN a.email ELSE a.nickname END, ', '), '') AS tower_emails
		FROM users u
		LEFT JOIN accounts a ON a.user_id = u.id
		GROUP BY u.id
		ORDER BY u.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []UserSummary{}
	for rows.Next() {
		var item UserSummary
		if err := rows.Scan(
			&item.ID, &item.Username, &item.DisplayName, &item.Role,
			&item.CreatedAt, &item.UpdatedAt, &item.TowerCount, &item.TowerEmails,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func resolveAccountID(user *UserInfo, token TokenResponse) string {
	if user != nil {
		if id := strings.TrimSpace(user.ID); id != "" {
			return id
		}
		if email := strings.TrimSpace(user.Email); email != "" {
			return email
		}
	}
	if email := strings.TrimSpace(token.Email); email != "" {
		return email
	}
	sum := sha256.Sum256([]byte(token.AccessToken))
	return "anon_" + hex.EncodeToString(sum[:8])
}

func scanAccount(row interface{ Scan(dest ...any) error }) (*StoredAccount, error) {
	var acc StoredAccount
	err := row.Scan(
		&acc.ID,
		&acc.UserID,
		&acc.TowerID,
		&acc.Nickname,
		&acc.Email,
		&acc.Avatar,
		&acc.AccessToken,
		&acc.RefreshToken,
		&acc.TokenType,
		&acc.ExpiresIn,
		&acc.TokenCreatedAt,
		&acc.MCPAPIKey,
		&acc.CreatedAt,
		&acc.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, err
	}
	if acc.TowerID == "" {
		acc.TowerID = acc.ID
	}
	return &acc, nil
}

const selectAccountCols = `id, user_id, tower_id, nickname, email, avatar, access_token, refresh_token, token_type, expires_in, token_created_at, mcp_api_key, created_at, updated_at`

func (s *Store) ListAccountsByUser(userID int64) ([]StoredAccount, error) {
	rows, err := s.db.Query(`SELECT `+selectAccountCols+` FROM accounts WHERE user_id = ? ORDER BY updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StoredAccount{}
	for rows.Next() {
		acc, scanErr := scanAccount(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, *acc)
	}
	return out, rows.Err()
}

func (s *Store) GetByID(id string) (*StoredAccount, error) {
	row := s.db.QueryRow(`SELECT `+selectAccountCols+` FROM accounts WHERE id = ?`, id)
	return scanAccount(row)
}

func (s *Store) GetByMCPKey(mcpKey string) (*StoredAccount, error) {
	key := strings.TrimSpace(mcpKey)
	if key == "" {
		return nil, ErrAccountNotFound
	}
	row := s.db.QueryRow(`SELECT `+selectAccountCols+` FROM accounts WHERE mcp_api_key = ?`, key)
	return scanAccount(row)
}

func (s *Store) GetByAccessToken(accessToken string) (*StoredAccount, error) {
	tok := strings.TrimSpace(accessToken)
	if tok == "" {
		return nil, ErrAccountNotFound
	}
	row := s.db.QueryRow(`SELECT `+selectAccountCols+` FROM accounts WHERE access_token = ?`, tok)
	return scanAccount(row)
}

func (s *Store) GetByAuth(credential string) (*StoredAccount, error) {
	cred := strings.TrimSpace(credential)
	if cred == "" {
		return nil, ErrAccountNotFound
	}
	if strings.HasPrefix(cred, "at_sk-") {
		return s.GetByMCPKey(cred)
	}
	if acc, err := s.GetByMCPKey(cred); err == nil {
		return acc, nil
	}
	return s.GetByAccessToken(cred)
}

func (s *Store) SaveLogin(user *UserInfo, token TokenResponse) (*StoredAccount, error) {
	return s.SaveLoginForUser(0, user, token)
}

func (s *Store) SaveLoginForUser(userID int64, user *UserInfo, token TokenResponse) (*StoredAccount, error) {
	return s.saveLogin(userID, user, token, nil, "")
}

// Both credentials become visible together, including when refreshing an existing binding.
func (s *Store) saveLogin(userID int64, user *UserInfo, token TokenResponse, web *webSessionRecord, rebindID string) (*StoredAccount, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	get := func(id string) (*StoredAccount, error) {
		return scanAccount(tx.QueryRow("SELECT "+selectAccountCols+" FROM accounts WHERE id = ?", id))
	}
	now := time.Now().Unix()
	towerID := resolveAccountID(user, token)
	id := towerID
	if userID > 0 {
		id = fmt.Sprintf("u%d:%s", userID, towerID)
	}
	if rebindID != "" {
		id = rebindID
	}
	createdAt := token.CreatedAt
	if createdAt <= 0 {
		createdAt = now
	}
	tokenType := strings.TrimSpace(token.TokenType)
	if tokenType == "" {
		tokenType = "bearer"
	}
	nickname, email, avatar := "", strings.TrimSpace(token.Email), ""
	if user != nil {
		if user.Nickname != "" {
			nickname = user.Nickname
		}
		if user.Email != "" {
			email = user.Email
		}
		if user.Avatar != "" {
			avatar = user.Avatar
		}
	}

	existing, err := get(id)
	if err != nil && !errors.Is(err, ErrAccountNotFound) {
		return nil, err
	}
	if rebindID != "" && (existing == nil || existing.UserID != userID || existing.TowerID != towerID) {
		return nil, errors.New("授权的 Tower 账号与要重新绑定的账号不一致，或原绑定已解除")
	}
	if existing == nil && userID > 0 {
		if legacy, lErr := get(towerID); lErr == nil && (legacy.UserID == 0 || legacy.UserID == userID) {
			existing = legacy
			id = legacy.ID
		}
	}
	if existing != nil {
		refreshToken := strings.TrimSpace(token.RefreshToken)
		if refreshToken == "" {
			refreshToken = existing.RefreshToken
		}
		if nickname == "" {
			nickname = existing.Nickname
		}
		if email == "" {
			email = existing.Email
		}
		if avatar == "" {
			avatar = existing.Avatar
		}
		_, err = tx.Exec(
			`UPDATE accounts
			 SET user_id = ?, tower_id = ?, nickname = ?, email = ?, avatar = ?, access_token = ?, refresh_token = ?,
			     token_type = ?, expires_in = ?, token_created_at = ?, updated_at = ?
			 WHERE id = ?`,
			userID, towerID, nickname, email, avatar, token.AccessToken, refreshToken,
			tokenType, token.ExpiresIn, createdAt, now, id,
		)
		if err != nil {
			return nil, err
		}
	} else {

		mcpKey, err := generateMCPKey()
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(
			`INSERT INTO accounts (id, user_id, tower_id, nickname, email, avatar, access_token, refresh_token, token_type, expires_in, token_created_at, mcp_api_key, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, userID, towerID, nickname, email, avatar, token.AccessToken, strings.TrimSpace(token.RefreshToken),
			tokenType, token.ExpiresIn, createdAt, mcpKey, now, now,
		)
		if err != nil {
			return nil, err
		}
	}
	if web != nil {
		if err := saveWebSession(tx, id, web.Cookies, web.Email, web.MemberGUID); err != nil {
			return nil, err
		}
	}
	acc, err := get(id)
	if err != nil {
		return nil, err
	}
	return acc, tx.Commit()
}

func (s *Store) UpdateTokens(id string, token TokenResponse) (*StoredAccount, error) {
	existing, err := s.GetByID(id)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	createdAt := token.CreatedAt
	if createdAt <= 0 {
		createdAt = now
	}
	refreshToken := strings.TrimSpace(token.RefreshToken)
	if refreshToken == "" {
		refreshToken = existing.RefreshToken
	}
	tokenType := strings.TrimSpace(token.TokenType)
	if tokenType == "" {
		tokenType = existing.TokenType
	}
	expiresIn := token.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = existing.ExpiresIn
	}
	_, err = s.db.Exec(
		`UPDATE accounts
		 SET access_token = ?, refresh_token = ?, token_type = ?, expires_in = ?, token_created_at = ?, updated_at = ?
		 WHERE id = ?`,
		token.AccessToken, refreshToken, tokenType, expiresIn, createdAt, now, id,
	)
	if err != nil {
		return nil, err
	}
	return s.GetByID(id)
}

func (s *Store) UpdateTokensByRefresh(oldRefreshToken string, token TokenResponse) (*StoredAccount, error) {
	row := s.db.QueryRow(`SELECT `+selectAccountCols+` FROM accounts WHERE refresh_token = ?`, strings.TrimSpace(oldRefreshToken))
	acc, err := scanAccount(row)
	if err != nil {
		return nil, err
	}
	return s.UpdateTokens(acc.ID, token)
}

func (s *Store) RotateMCPKey(credential string) (*StoredAccount, error) {
	return s.RotateMCPKeyForUser(0, credential)
}

func (s *Store) RotateMCPKeyForUser(userID int64, credential string) (*StoredAccount, error) {
	acc, err := s.GetByAuth(credential)
	if err != nil {
		return nil, err
	}
	if userID > 0 && acc.UserID != 0 && acc.UserID != userID {
		return nil, ErrAccountNotFound
	}
	newKey, err := generateMCPKey()
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(`UPDATE accounts SET mcp_api_key = ?, updated_at = ? WHERE id = ?`, newKey, time.Now().Unix(), acc.ID)
	if err != nil {
		return nil, err
	}
	return s.GetByID(acc.ID)
}

func (s *Store) DeleteByAuth(credential string) error {
	return s.DeleteByAuthForUser(0, credential)
}

func (s *Store) DeleteByAuthForUser(userID int64, credential string) error {
	acc, err := s.GetByAuth(credential)
	if err != nil {
		return err
	}
	if userID > 0 && acc.UserID != 0 && acc.UserID != userID {
		return ErrAccountNotFound
	}
	_, err = s.db.Exec(`DELETE FROM accounts WHERE id = ?`, acc.ID)
	return err
}

func (s *Store) EnsureFreshToken(cfg Config, mcpKey string, force bool) (*StoredAccount, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	acc, err := s.GetByMCPKey(mcpKey)
	if err != nil {
		return nil, err
	}
	if !force && !acc.NeedsRefresh(time.Now()) {
		return acc, nil
	}
	if acc.RefreshToken == "" {
		if force {
			return nil, errors.New("Tower Access Token 已失效且无可用 Refresh Token，请重新登录 AgentsTower")
		}
		return acc, nil
	}
	form := refreshTokenForm(cfg, acc.RefreshToken)
	tokenResp, err := postToken(cfg, form, acc.AccessToken)
	if err != nil {
		return nil, errors.New("自动刷新 Tower Token 失败: " + err.Error())
	}
	if tokenResp.Error != "" {
		return nil, errors.New("自动刷新 Tower Token 失败: " + formatOAuthError(tokenResp.Error, tokenResp.ErrorDescription))
	}
	return s.UpdateTokens(acc.ID, tokenResp)
}

func (s *Store) SaveWebSession(accountID string, cookies []webCookie, email, memberGUID string) error {
	return saveWebSession(s.db, accountID, cookies, email, memberGUID)
}

func saveWebSession(db interface {
	Exec(string, ...any) (sql.Result, error)
}, accountID string, cookies []webCookie, email, memberGUID string) error {
	raw, err := json.Marshal(cookies)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	_, err = db.Exec(
		`INSERT INTO web_sessions (account_id, cookies, email, member_guid, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(account_id) DO UPDATE SET cookies = excluded.cookies, email = excluded.email,
		 member_guid = excluded.member_guid, updated_at = excluded.updated_at`,
		accountID, string(raw), email, memberGUID, now,
	)
	return err
}

func (s *Store) GetWebSession(accountID string) (webSessionRecord, error) {
	var raw, email, member string
	err := s.db.QueryRow(`SELECT cookies, email, member_guid FROM web_sessions WHERE account_id = ?`, accountID).Scan(&raw, &email, &member)
	if errors.Is(err, sql.ErrNoRows) {
		return webSessionRecord{}, errNoWebSession
	}
	if err != nil {
		return webSessionRecord{}, err
	}
	if strings.TrimSpace(raw) == "" || raw == "null" || raw == "[]" {
		return webSessionRecord{}, errNoWebSession
	}
	var cookies []webCookie
	if err := json.Unmarshal([]byte(raw), &cookies); err != nil {
		return webSessionRecord{}, err
	}
	if len(cookies) == 0 {
		return webSessionRecord{}, errNoWebSession
	}
	return webSessionRecord{Cookies: cookies, Email: email, MemberGUID: member}, nil
}
