package db

import (
	"context"
	"time"

	"github.com/dresswithpockets/go-steam-openid-example/db/query"
	"github.com/rotisserie/eris"
)

var maxNonceAge = 60 * time.Second

type NonceStore struct {
	ctx     context.Context
	queries *query.Queries
}

func NewNonceStore(ctx context.Context, queries *query.Queries) *NonceStore {
	return &NonceStore{
		ctx:     ctx,
		queries: queries,
	}
}

func (n NonceStore) Accept(endpoint, nonce string) error {
	// Value: A string 255 characters or less in length, that MUST be
	// unique to this particular successful authentication response.
	if len(nonce) < 20 || len(nonce) > 256 {
		return eris.New("invalid nonce")
	}

	// The nonce MUST start with the current time on the server, and MAY
	// contain additional ASCII characters in the range 33-126 inclusive
	// (printable non-whitespace characters), as necessary to make each
	// response unique. The date and time MUST be formatted as specified in
	// section 5.6 of [RFC3339], with the following restrictions:

	// All times must be in the UTC timezone, indicated with a "Z".  No
	// fractional seconds are allowed For example:
	// 2005-05-15T17:11:51ZUNIQUE
	ts, err := time.Parse(time.RFC3339, nonce[0:20])
	if err != nil {
		return eris.Wrap(err, "invalid nonce")
	}
	now := time.Now()
	diff := now.Sub(ts)
	if diff > maxNonceAge {
		return eris.Errorf("nonce too old: %.2fs", diff.Seconds())
	}

	s := nonce[20:]

	insertErr := n.queries.InsertNonce(n.ctx, query.InsertNonceParams{
		Endpoint:    endpoint,
		NonceTime:   ts,
		NonceString: s,
	})

	if insertErr != nil {
		return eris.Wrap(insertErr, "failed to insert nonce")
	}

	return nil
}
