package pkg

import "context"

type userIdKey struct{}

// WithUserId returns a context carrying the signed-in user's id. The auth
// middleware sets it; handlers read it with UserIdFrom.
func WithUserId(ctx context.Context, userId string) context.Context {
	return context.WithValue(ctx, userIdKey{}, userId)
}

// UserIdFrom returns the signed-in user's id, or "" if there is none.
func UserIdFrom(ctx context.Context) string {
	id, _ := ctx.Value(userIdKey{}).(string)
	return id
}
