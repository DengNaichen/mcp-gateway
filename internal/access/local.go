package access

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
)

// LocalAdminAuth verifies the credential for the local management channel.
type LocalAdminAuth struct {
	digest [sha256.Size]byte
}

func NewLocalAdminAuth(token string) *LocalAdminAuth {
	if token == "" {
		return nil
	}
	return &LocalAdminAuth{digest: sha256.Sum256([]byte(token))}
}

func (a *LocalAdminAuth) Authenticate(token string) bool {
	digest := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(a.digest[:], digest[:]) == 1
}

type localAdminContextKey struct{}

// WithLocalAdmin marks a request verified by the local management channel.
// It leaves the simulated actor used for local tool ACLs intact.
func WithLocalAdmin(ctx context.Context) context.Context {
	return context.WithValue(ctx, localAdminContextKey{}, true)
}

func IsLocalAdmin(ctx context.Context) bool {
	admin, _ := ctx.Value(localAdminContextKey{}).(bool)
	return admin
}
