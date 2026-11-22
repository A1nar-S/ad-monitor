package subscriptions

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/A1nar-S/advertisiment-monitoring/internal/subscriptions/subscriptionsdb"
)

// A user is one person; each chat account (Discord, Telegram, ...) they use
// the bot from belongs to one user. Linking (a code from /link on one app,
// redeemed with /link CODE on the other) joins two users into one.

// LinkCodeTTL is how long a link code works.
const LinkCodeTTL = 10 * time.Minute

// ErrSameUser: the code was made by this very user (or one already linked).
var ErrSameUser = errors.New("already linked")

// Account is one chat account of a user.
type Account struct{ Platform, ExternalID, Name string }

// LinkResult says what linking did.
type LinkResult struct {
	// Accounts of the user now, the caller's included.
	Accounts []Account
	// Searches and Watches moved over from the caller's former user.
	Searches, Watches int64
}

// PlatformOwner returns the user for a chat account, creating it on first
// contact.
func (s *Service) PlatformOwner(ctx context.Context, platform, externalID, displayName string) (Owner, error) {
	id, err := s.q.AccountUser(ctx, subscriptionsdb.AccountUserParams{
		Platform: platform, ExternalID: externalID, DisplayName: displayName,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		id, err = s.createAccount(ctx, platform, externalID, displayName)
	}
	if err != nil {
		return Owner{}, fmt.Errorf("user: %w", err)
	}
	return s.owner(ctx, id, platform, externalID, displayName)
}

// createAccount makes the user and account of a first contact. Two first
// contacts at once agree: the account's key decides which user it is.
func (s *Service) createAccount(ctx context.Context, platform, externalID, displayName string) (int64, error) {
	id, err := s.q.CreatePlatformUser(ctx, subscriptionsdb.CreatePlatformUserParams{
		Platform: platform, ExternalID: externalID, DisplayName: displayName,
	})
	if err != nil {
		return 0, err
	}
	err = s.q.InsertAccount(ctx, subscriptionsdb.InsertAccountParams{
		Platform: platform, ExternalID: externalID, UserID: id, DisplayName: displayName,
	})
	if err != nil {
		return 0, err
	}
	return s.q.AccountUser(ctx, subscriptionsdb.AccountUserParams{
		Platform: platform, ExternalID: externalID, DisplayName: displayName,
	})
}

// owner builds the Owner of user id, acting through the given chat account
// (empty from the dashboard). Admin rights come with any of the user's
// accounts.
func (s *Service) owner(ctx context.Context, id int64, platform, externalID, name string) (Owner, error) {
	o := Owner{UserID: id, Platform: platform, ExternalID: externalID, Name: name, Admin: s.admins[platform+":"+externalID]}
	if o.Admin || len(s.admins) == 0 {
		return o, nil
	}
	accounts, err := s.Accounts(ctx, o)
	if err != nil {
		return Owner{}, err
	}
	for _, a := range accounts {
		o.Admin = o.Admin || s.admins[a.Platform+":"+a.ExternalID]
	}
	return o, nil
}

// Accounts lists the owner's chat accounts, oldest first.
func (s *Service) Accounts(ctx context.Context, o Owner) ([]Account, error) {
	rows, err := s.q.UserAccounts(ctx, o.UserID)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}
	out := make([]Account, len(rows))
	for i, r := range rows {
		out[i] = Account{Platform: r.Platform, ExternalID: r.ExternalID, Name: r.DisplayName}
	}
	return out, nil
}

// NewLinkCode issues a one-time code ("ABCD-EFGH") that links another chat
// account to the owner.
func (s *Service) NewLinkCode(ctx context.Context, o Owner) (string, error) {
	code, err := newLinkCode()
	if err != nil {
		return "", err
	}
	err = s.q.InsertLinkCode(ctx, subscriptionsdb.InsertLinkCodeParams{
		CodeHash: hashToken(normalizeCode(code)), UserID: o.UserID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(LinkCodeTTL), Valid: true},
	})
	return code, err
}

// LinkTarget says whose accounts a code would link the owner to, without
// using it up: the user confirms before Link.
func (s *Service) LinkTarget(ctx context.Context, o Owner, code string) ([]Account, error) {
	keeper, err := s.q.PeekLinkCode(ctx, hashToken(normalizeCode(code)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBadToken
	}
	if err != nil {
		return nil, err
	}
	if keeper == o.UserID {
		return nil, ErrSameUser
	}
	return s.Accounts(ctx, Owner{UserID: keeper})
}

// Link redeems a link code: the owner's user, with all its accounts,
// searches, watches and channels, joins the user who made the code.
func (s *Service) Link(ctx context.Context, o Owner, code string) (LinkResult, error) {
	var res LinkResult
	var keeper int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var err error
		keeper, err = q.ConsumeLinkCode(ctx, hashToken(normalizeCode(code)))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBadToken
		}
		if err != nil {
			return err
		}
		if keeper == o.UserID {
			return ErrSameUser
		}
		res.Searches, res.Watches, err = merge(ctx, q, keeper, o.UserID)
		return err
	})
	if err != nil {
		return LinkResult{}, err
	}
	res.Accounts, err = s.Accounts(ctx, Owner{UserID: keeper})
	return res, err
}

// merge moves everything of user absorbed to keeper and deletes absorbed.
func merge(ctx context.Context, q *subscriptionsdb.Queries, keeper, absorbed int64) (searches, watches int64, err error) {
	steps := []func() error{
		func() error {
			return q.RepointSearchChannels(ctx, subscriptionsdb.RepointSearchChannelsParams{Keeper: keeper, Absorbed: absorbed})
		},
		func() error {
			return q.RepointWatchChannels(ctx, subscriptionsdb.RepointWatchChannelsParams{Keeper: keeper, Absorbed: absorbed})
		},
		func() error {
			return q.DeleteDuplicateChannels(ctx, subscriptionsdb.DeleteDuplicateChannelsParams{Keeper: keeper, Absorbed: absorbed})
		},
		func() error {
			return q.MoveChannels(ctx, subscriptionsdb.MoveChannelsParams{Keeper: keeper, Absorbed: absorbed})
		},
		func() error {
			return q.RenameClashingSearches(ctx, subscriptionsdb.RenameClashingSearchesParams{Keeper: keeper, Absorbed: absorbed})
		},
		func() (err error) {
			searches, err = q.MoveSearches(ctx, subscriptionsdb.MoveSearchesParams{Keeper: keeper, Absorbed: absorbed})
			return err
		},
		func() error {
			return q.RenameClashingWatches(ctx, subscriptionsdb.RenameClashingWatchesParams{Keeper: keeper, Absorbed: absorbed})
		},
		func() (err error) {
			watches, err = q.MoveWatches(ctx, subscriptionsdb.MoveWatchesParams{Keeper: keeper, Absorbed: absorbed})
			return err
		},
		func() error {
			return q.MoveAccounts(ctx, subscriptionsdb.MoveAccountsParams{Keeper: keeper, Absorbed: absorbed})
		},
		func() error {
			return q.MoveSessions(ctx, subscriptionsdb.MoveSessionsParams{Keeper: keeper, Absorbed: absorbed})
		},
		func() error { return q.DeleteUser(ctx, absorbed) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return 0, 0, fmt.Errorf("link: %w", err)
		}
	}
	return searches, watches, nil
}

// codeAlphabet leaves out look-alikes (0/O, 1/I/L).
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// newLinkCode makes 8 random characters, shown as ABCD-EFGH: short enough
// to type, and with a 10-minute life far too many to guess.
func newLinkCode() (string, error) {
	out := make([]byte, 0, 9)
	buf := make([]byte, 16)
	for len(out) < 9 {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, c := range buf {
			// Bytes past the last whole multiple of the alphabet would
			// favour its first letters.
			if len(out) == 9 || int(c) >= 256/len(codeAlphabet)*len(codeAlphabet) {
				continue
			}
			if len(out) == 4 {
				out = append(out, '-')
			}
			out = append(out, codeAlphabet[int(c)%len(codeAlphabet)])
		}
	}
	return string(out), nil
}

// normalizeCode forgives case, spaces and dashes: "abcd efgh" = "ABCD-EFGH".
func normalizeCode(code string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '-' || r == ' ' || r == '\t':
			return -1
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		}
		return r
	}, code)
}
