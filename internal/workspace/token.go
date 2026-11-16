package workspace

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"gatewaylab/internal/buildinfo"
)

const (
	TokenVersion   = 1
	TokenTTL       = 60 * time.Second
	ScopeKeyLength = 32
	ScopeKeyFile   = "scope.key"
)

var (
	ErrScopeKeyMissing = errors.New("scope key is not initialized")
	ErrTokenInvalid    = errors.New("scope token is invalid")
)

var KnownAgents = []string{"codex", "claude", "cursor", "opencode", "pi"}

func IsKnownAgent(agent string) bool {
	return slices.Contains(KnownAgents, agent)
}

// ContextFieldForSlug returns the channel-isolated top-level argument key:
// "_gw_ctx" for "mcp-gateway" and "_gw_ctx_mcp_gateway_dev" for "mcp-gateway-dev".
func ContextFieldForSlug(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" || slug == "mcp-gateway" {
		return "_gw_ctx"
	}
	var b strings.Builder
	b.WriteString("_gw_ctx_")
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// CurrentContextField returns the reserved top-level field for the compiled channel.
func CurrentContextField() string {
	return ContextFieldForSlug(buildinfo.Current().Slug)
}

// IsReservedContextField reports whether key is any Gateway context field (_gw_ctx or _gw_ctx_*).
func IsReservedContextField(key string) bool {
	return key == "_gw_ctx" || strings.HasPrefix(key, "_gw_ctx_")
}

type Claims struct {
	V         int    `json:"v"`
	Channel   string `json:"channel"`
	Agent     string `json:"agent"`
	Cwd       string `json:"cwd,omitempty"`
	Ambiguous bool   `json:"ambiguous,omitempty"`
	Op        string `json:"op"`
	ArgsHash  string `json:"args_hash"`
	SID       string `json:"sid,omitempty"`
	IAT       int64  `json:"iat"`
	EXP       int64  `json:"exp"`
	JTI       string `json:"jti"`
}

type VerifyParams struct {
	Channel    string
	URLAgent   string
	AllowedOps []string
	ArgsHash   string
}

func NewJTI() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate jti: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// SignToken encodes and signs Claims into "v1.<b64url(claims)>.<b64url(hmac_sha256)>".
func SignToken(key []byte, claims Claims) (string, error) {
	if len(key) != ScopeKeyLength {
		return "", fmt.Errorf("invalid scope key length %d", len(key))
	}
	if claims.V == 0 {
		claims.V = TokenVersion
	}
	if claims.Channel == "" {
		claims.Channel = buildinfo.Current().Channel
	}
	if !IsKnownAgent(claims.Agent) {
		return "", fmt.Errorf("unknown agent %q", claims.Agent)
	}
	if claims.Op == "" || claims.ArgsHash == "" {
		return "", errors.New("claims require op and args_hash")
	}
	if !claims.Ambiguous && strings.TrimSpace(claims.Cwd) == "" {
		return "", errors.New("non-ambiguous claims require cwd")
	}
	now := time.Now().Unix()
	if claims.IAT == 0 {
		claims.IAT = now
	}
	if claims.EXP == 0 {
		claims.EXP = claims.IAT + int64(TokenTTL/time.Second)
	}
	if claims.JTI == "" {
		jti, err := NewJTI()
		if err != nil {
			return "", err
		}
		claims.JTI = jti
	}

	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := "v1." + payloadB64

	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(signingInput))
	sigB64 := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + sigB64, nil
}

// VerifyToken verifies the HMAC signature and claim invariants of a v1 scope token.
func VerifyToken(key []byte, token string, expected VerifyParams, now time.Time) (Claims, error) {
	if len(key) != ScopeKeyLength {
		return Claims{}, fmt.Errorf("%w: key not ready", ErrTokenInvalid)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" || parts[1] == "" || parts[2] == "" {
		return Claims{}, fmt.Errorf("%w: malformed token envelope", ErrTokenInvalid)
	}
	// The decoder ignores \r and \n and accepts non-zero trailing bits; require the
	// canonical encoding so each signature has exactly one valid token string.
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != sha256.Size || base64.RawURLEncoding.EncodeToString(sig) != parts[2] {
		return Claims{}, fmt.Errorf("%w: invalid signature encoding", ErrTokenInvalid)
	}

	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(signingInput))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return Claims{}, fmt.Errorf("%w: signature mismatch", ErrTokenInvalid)
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: invalid payload encoding", ErrTokenInvalid)
	}
	dec := json.NewDecoder(bytes.NewReader(payloadBytes))
	dec.DisallowUnknownFields()
	var claims Claims
	if err := dec.Decode(&claims); err != nil {
		return Claims{}, fmt.Errorf("%w: invalid claims JSON", ErrTokenInvalid)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Claims{}, fmt.Errorf("%w: trailing claims data", ErrTokenInvalid)
	}

	if claims.V != TokenVersion {
		return Claims{}, fmt.Errorf("%w: unsupported version %d", ErrTokenInvalid, claims.V)
	}
	wantChannel := expected.Channel
	if wantChannel == "" {
		wantChannel = buildinfo.Current().Channel
	}
	if claims.Channel != wantChannel {
		return Claims{}, fmt.Errorf("%w: channel mismatch", ErrTokenInvalid)
	}
	if !IsKnownAgent(claims.Agent) {
		return Claims{}, fmt.Errorf("%w: unknown agent %q", ErrTokenInvalid, claims.Agent)
	}
	if expected.URLAgent != "" && claims.Agent != expected.URLAgent {
		return Claims{}, fmt.Errorf("%w: agent mismatch (%s != %s)", ErrTokenInvalid, claims.Agent, expected.URLAgent)
	}
	if len(expected.AllowedOps) > 0 && !slices.Contains(expected.AllowedOps, claims.Op) {
		return Claims{}, fmt.Errorf("%w: operation mismatch", ErrTokenInvalid)
	}
	if expected.ArgsHash != "" && subtleCompareStrings(claims.ArgsHash, expected.ArgsHash) != 1 {
		return Claims{}, fmt.Errorf("%w: arguments hash mismatch", ErrTokenInvalid)
	}
	if claims.JTI == "" || len(claims.JTI) > 128 {
		return Claims{}, fmt.Errorf("%w: invalid jti", ErrTokenInvalid)
	}
	nowUnix := now.Unix()
	if claims.IAT <= 0 || claims.EXP <= claims.IAT || claims.EXP-claims.IAT > 300 {
		return Claims{}, fmt.Errorf("%w: invalid token lifetime", ErrTokenInvalid)
	}
	if claims.IAT > nowUnix+5 {
		return Claims{}, fmt.Errorf("%w: token issued in the future", ErrTokenInvalid)
	}
	if nowUnix > claims.EXP {
		return Claims{}, fmt.Errorf("%w: token expired", ErrTokenInvalid)
	}
	if !claims.Ambiguous {
		if strings.TrimSpace(claims.Cwd) == "" || !filepath.IsAbs(claims.Cwd) {
			return Claims{}, fmt.Errorf("%w: invalid cwd", ErrTokenInvalid)
		}
	}
	return claims, nil
}

func subtleCompareStrings(a, b string) int {
	if len(a) != len(b) {
		return 0
	}
	if hmac.Equal([]byte(a), []byte(b)) {
		return 1
	}
	return 0
}

// CanonicalArgsHash computes the hex SHA-256 of canonical JSON of args after
// excluding reserved Gateway context fields (_gw_ctx / _gw_ctx_*) so Stable and
// Dev hooks coexisting on the same host (e.g. Cursor preToolUse) verify cleanly
// regardless of hook execution order.
func CanonicalArgsHash(args map[string]any, channelField string) (string, error) {
	filtered := make(map[string]any, len(args))
	for k, v := range args {
		if IsReservedContextField(k) || (channelField != "" && k == channelField) {
			continue
		}
		norm, err := normalizeCanonicalValue(v)
		if err != nil {
			return "", err
		}
		filtered[k] = norm
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(filtered); err != nil {
		return "", fmt.Errorf("encode canonical args: %w", err)
	}
	canonical := bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// CanonicalArgsHashJSON parses raw JSON object (treating empty/null as {}) and
// returns the hex SHA-256 of canonical JSON after removing ONLY channelField,
// along with the extracted string value of channelField (if present) and whether
// channelField existed in the object.
func CanonicalArgsHashJSON(raw []byte, channelField string) (hash string, tokenVal string, hasField bool, err error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		h, e := CanonicalArgsHash(nil, channelField)
		return h, "", false, e
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var parsed any
	if err := dec.Decode(&parsed); err != nil {
		return "", "", false, fmt.Errorf("decode tool arguments: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", "", false, errors.New("trailing JSON in tool arguments")
	}
	obj, ok := parsed.(map[string]any)
	if !ok {
		return "", "", false, errors.New("tool arguments must be a JSON object")
	}
	if channelField != "" {
		if rawCtx, exists := obj[channelField]; exists {
			hasField = true
			s, ok := rawCtx.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return "", "", true, fmt.Errorf("%w: %s must be a non-empty string", ErrTokenInvalid, channelField)
			}
			tokenVal = s
		}
	}
	h, err := CanonicalArgsHash(obj, channelField)
	if err != nil {
		return "", "", hasField, err
	}
	return h, tokenVal, hasField, nil
}

// StripContextFieldsJSON removes _gw_ctx and all _gw_ctx_* top-level keys from raw JSON arguments.
func StripContextFieldsJSON(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return []byte("{}"), nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var parsed any
	if err := dec.Decode(&parsed); err != nil {
		return nil, err
	}
	obj, ok := parsed.(map[string]any)
	if !ok {
		return raw, nil
	}
	changed := false
	for k := range obj {
		if IsReservedContextField(k) {
			delete(obj, k)
			changed = true
		}
	}
	if !changed {
		return raw, nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(obj); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}), nil
}

func normalizeCanonicalValue(v any) (any, error) {
	switch x := v.(type) {
	case nil, bool, string:
		return x, nil
	case json.Number:
		return canonicalJSONNumber(string(x))
	case int:
		return json.Number(strconv.FormatInt(int64(x), 10)), nil
	case int64:
		return json.Number(strconv.FormatInt(x, 10)), nil
	case int32:
		return json.Number(strconv.FormatInt(int64(x), 10)), nil
	case uint:
		return json.Number(strconv.FormatUint(uint64(x), 10)), nil
	case uint64:
		return json.Number(strconv.FormatUint(x, 10)), nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, errors.New("non-finite float in JSON")
		}
		if x == 0 {
			return json.Number("0"), nil
		}
		if trunc := math.Trunc(x); trunc == x && x >= -9007199254740992 && x <= 9007199254740992 {
			return json.Number(strconv.FormatInt(int64(x), 10)), nil
		}
		return json.Number(strconv.FormatFloat(x, 'g', -1, 64)), nil
	case float32:
		return normalizeCanonicalValue(float64(x))
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, elem := range x {
			n, err := normalizeCanonicalValue(elem)
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, elem := range x {
			n, err := normalizeCanonicalValue(elem)
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	default:
		// Re-marshal arbitrary typed structs/slices via UseNumber so all Go types normalize identically.
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		var generic any
		if err := dec.Decode(&generic); err != nil {
			return nil, err
		}
		return normalizeCanonicalValue(generic)
	}
}

func canonicalJSONNumber(raw string) (json.Number, error) {
	if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return json.Number(strconv.FormatInt(i, 10)), nil
	}
	if u, err := strconv.ParseUint(raw, 10, 64); err == nil {
		return json.Number(strconv.FormatUint(u, 10)), nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return "", fmt.Errorf("invalid JSON number %q", raw)
	}
	if f == 0 {
		return json.Number("0"), nil
	}
	if trunc := math.Trunc(f); trunc == f && f >= -9007199254740992 && f <= 9007199254740992 {
		return json.Number(strconv.FormatInt(int64(f), 10)), nil
	}
	return json.Number(strconv.FormatFloat(f, 'g', -1, 64)), nil
}

// DefaultDataDir returns ~/.<slug>/data for the compiled build channel.
func DefaultDataDir() (string, error) {
	home, err := resolveHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "."+buildinfo.Current().Slug, "data"), nil
}

// ResolveDataDir resolves the Gateway data directory in order:
// 1. explicit (--data)
// 2. channel-specific env var (GATEWAY_DEV_DATA_DIR / GATEWAY_DATA_DIR)
// 3. GATEWAY_DATA_DIR
// 4. ~/.<slug>/data
func ResolveDataDir(explicit string) (string, error) {
	if trimmed := strings.TrimSpace(explicit); trimmed != "" {
		return filepath.Abs(trimmed)
	}
	if fromChEnv := strings.TrimSpace(os.Getenv(buildinfo.EnvironmentKey("GATEWAY_DATA_DIR"))); fromChEnv != "" {
		return filepath.Abs(fromChEnv)
	}
	if fromEnv := strings.TrimSpace(os.Getenv("GATEWAY_DATA_DIR")); fromEnv != "" {
		return filepath.Abs(fromEnv)
	}
	return DefaultDataDir()
}

func ScopeKeyPath(dataDir string) string {
	return filepath.Join(dataDir, ScopeKeyFile)
}

// EnsureScopeKey loads or atomically initializes the 32-byte 0600 HMAC scope.key in dataDir.
func EnsureScopeKey(dataDir string) ([]byte, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("empty dataDir for scope.key")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("create dataDir: %w", err)
	}
	keyPath := ScopeKeyPath(dataDir)
	if key, err := readExistingScopeKey(keyPath); err == nil {
		return key, nil
	} else if !errors.Is(err, ErrScopeKeyMissing) {
		return nil, err
	}

	secret := make([]byte, ScopeKeyLength)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate scope.key: %w", err)
	}

	// Exclusive create avoids clobbering a concurrent process's newly created key.
	f, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return readExistingScopeKey(keyPath)
		}
		return nil, fmt.Errorf("create scope.key: %w", err)
	}
	if _, writeErr := f.Write(secret); writeErr != nil {
		_ = f.Close()
		_ = os.Remove(keyPath)
		return nil, fmt.Errorf("write scope.key: %w", writeErr)
	}
	if syncErr := f.Sync(); syncErr != nil {
		_ = f.Close()
		_ = os.Remove(keyPath)
		return nil, fmt.Errorf("sync scope.key: %w", syncErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		return nil, fmt.Errorf("close scope.key: %w", closeErr)
	}
	if chmodErr := os.Chmod(keyPath, 0600); chmodErr != nil {
		return nil, fmt.Errorf("chmod scope.key: %w", chmodErr)
	}
	return secret, nil
}

// LoadScopeKey loads the 32-byte HMAC key from the resolved dataDir. When neither
// explicit nor GATEWAY_DATA_DIR is set and ~/.<slug>/data/scope.key is absent,
// it also checks <UserConfigDir>/<slug>/scope.key so desktop launches using OS
// config directories work out-of-the-box.
func LoadScopeKey(explicitDataDir string) ([]byte, error) {
	hasExplicit := strings.TrimSpace(explicitDataDir) != "" ||
		strings.TrimSpace(os.Getenv(buildinfo.EnvironmentKey("GATEWAY_DATA_DIR"))) != "" ||
		strings.TrimSpace(os.Getenv("GATEWAY_DATA_DIR")) != ""

	dir, err := ResolveDataDir(explicitDataDir)
	if err != nil {
		return nil, err
	}
	key, err := readExistingScopeKey(ScopeKeyPath(dir))
	if err == nil || hasExplicit || !errors.Is(err, ErrScopeKeyMissing) {
		return key, err
	}

	if cfgDir, cfgErr := os.UserConfigDir(); cfgErr == nil && cfgDir != "" {
		alt := ScopeKeyPath(filepath.Join(cfgDir, buildinfo.Current().Slug))
		if altKey, altErr := readExistingScopeKey(alt); altErr == nil {
			return altKey, nil
		}
	}
	return nil, ErrScopeKeyMissing
}

func readExistingScopeKey(keyPath string) ([]byte, error) {
	info, err := os.Lstat(keyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrScopeKeyMissing
		}
		return nil, fmt.Errorf("stat scope.key: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("scope.key is not a regular file")
	}
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read scope.key: %w", err)
	}
	if len(raw) != ScopeKeyLength {
		return nil, fmt.Errorf("invalid scope.key size %d (expected %d)", len(raw), ScopeKeyLength)
	}
	if info.Mode().Perm() != 0600 {
		_ = os.Chmod(keyPath, 0600)
	}
	return raw, nil
}

func resolveHomeDir() (string, error) {
	if h := strings.TrimSpace(os.Getenv("HOME")); h != "" {
		return filepath.Clean(h), nil
	}
	h, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(h) == "" {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Clean(h), nil
}
