package journey

import (
	"context"
	"errors"
)

var ErrUnauthorized = errors.New("unauthorized")

type Journey interface {
	Name() string
	Execute(ctx context.Context, token string) error
}
