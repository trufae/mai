package deltachat

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// DefaultChatmailRelay is used when CreateAccount receives no login or relay.
const DefaultChatmailRelay = "nine.testrun.org"

// Accounts lists all profiles in the account store.
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	var accounts []Account
	if err := c.Call(ctx, "get_all_accounts", &accounts); err != nil {
		return nil, fmt.Errorf("list Delta Chat accounts: %w", err)
	}
	return accounts, nil
}

// Account returns one profile.
func (c *Client) Account(ctx context.Context, accountID uint32) (Account, error) {
	var account Account
	if err := c.Call(ctx, "get_account_info", &account, accountID); err != nil {
		return Account{}, fmt.Errorf("get Delta Chat account %d: %w", accountID, err)
	}
	return account, nil
}

// SelectedAccountID returns the account manager's persisted selection.
func (c *Client) SelectedAccountID(ctx context.Context) (*uint32, error) {
	var accountID *uint32
	if err := c.Call(ctx, "get_selected_account_id", &accountID); err != nil {
		return nil, fmt.Errorf("get selected Delta Chat account: %w", err)
	}
	return accountID, nil
}

// SelectAccount persists the active account used when callers omit account_id.
func (c *Client) SelectAccount(ctx context.Context, accountID uint32) error {
	if err := c.Call(ctx, "select_account", nil, accountID); err != nil {
		return fmt.Errorf("select Delta Chat account %d: %w", accountID, err)
	}
	return nil
}

// ResolveAccount returns an explicit account or a useful configured default.
func (c *Client) ResolveAccount(ctx context.Context, requested uint32, configured bool) (uint32, error) {
	if requested != 0 {
		account, err := c.Account(ctx, requested)
		if err != nil {
			return 0, err
		}
		if configured && account.Kind != "Configured" {
			return 0, fmt.Errorf("Delta Chat account %d is not configured", requested)
		}
		return requested, nil
	}

	selected, err := c.SelectedAccountID(ctx)
	if err != nil {
		return 0, err
	}
	accounts, err := c.Accounts(ctx)
	if err != nil {
		return 0, err
	}
	if selected != nil {
		for _, account := range accounts {
			if account.ID == *selected && (!configured || account.Kind == "Configured") {
				return account.ID, nil
			}
		}
	}
	for _, account := range accounts {
		if !configured || account.Kind == "Configured" {
			if err := c.SelectAccount(ctx, account.ID); err != nil {
				return 0, err
			}
			return account.ID, nil
		}
	}
	if configured {
		return 0, fmt.Errorf("no configured Delta Chat account is available; create or configure an account first")
	}
	return 0, fmt.Errorf("no Delta Chat account is available; create an account first")
}

// CreateAccount creates, configures, and selects a profile. It removes the
// incomplete profile when configuration fails.
func (c *Client) CreateAccount(ctx context.Context, opts CreateAccountOptions) (Account, error) {
	var login *LoginParams
	var qr string
	if opts.Login != nil {
		copy := *opts.Login
		copy.Address = strings.TrimSpace(copy.Address)
		if copy.Address == "" || copy.Password == "" {
			return Account{}, fmt.Errorf("email and password are required for a conventional Delta Chat account")
		}
		login = &copy
	} else {
		var err error
		qr, err = chatmailAccountQR(opts.Relay)
		if err != nil {
			return Account{}, err
		}
	}
	if opts.ProfileImage != "" {
		profileImage, err := regularFilePath(opts.ProfileImage)
		if err != nil {
			return Account{}, fmt.Errorf("invalid Delta Chat profile image: %w", err)
		}
		opts.ProfileImage = profileImage
	}

	var accountID uint32
	if err := c.Call(ctx, "add_account", &accountID); err != nil {
		return Account{}, fmt.Errorf("add Delta Chat account: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = c.Call(cleanupCtx, "remove_account", nil, accountID)
			cancel()
		}
	}()

	var err error
	if login != nil {
		err = c.Call(ctx, "add_or_update_transport", nil, accountID, login)
	} else {
		err = c.Call(ctx, "add_transport_from_qr", nil, accountID, qr)
	}
	if err != nil {
		return Account{}, fmt.Errorf("configure Delta Chat account: %w", err)
	}
	if err := c.SetProfile(ctx, accountID, opts.DisplayName, opts.ProfileImage); err != nil {
		return Account{}, err
	}
	if err := c.SelectAccount(ctx, accountID); err != nil {
		return Account{}, err
	}
	account, err := c.Account(ctx, accountID)
	if err != nil {
		return Account{}, err
	}
	cleanup = false
	return account, nil
}

// RemoveAccount permanently removes an account and its local data.
func (c *Client) RemoveAccount(ctx context.Context, accountID uint32) error {
	if err := c.Call(ctx, "remove_account", nil, accountID); err != nil {
		return fmt.Errorf("remove Delta Chat account %d: %w", accountID, err)
	}
	return nil
}

// SetProfile updates non-empty profile fields.
func (c *Client) SetProfile(ctx context.Context, accountID uint32, displayName, profileImage string) error {
	if displayName = strings.TrimSpace(displayName); displayName != "" {
		if err := c.Call(ctx, "set_config", nil, accountID, "displayname", displayName); err != nil {
			return fmt.Errorf("set Delta Chat display name: %w", err)
		}
	}
	if profileImage = strings.TrimSpace(profileImage); profileImage != "" {
		abs, err := regularFilePath(profileImage)
		if err != nil {
			return fmt.Errorf("invalid Delta Chat profile image: %w", err)
		}
		if err := c.Call(ctx, "set_config", nil, accountID, "selfavatar", abs); err != nil {
			return fmt.Errorf("set Delta Chat profile image: %w", err)
		}
	}
	return nil
}

// StartIO starts background network processing for an account.
func (c *Client) StartIO(ctx context.Context, accountID uint32) error {
	if err := c.Call(ctx, "start_io", nil, accountID); err != nil {
		return fmt.Errorf("start Delta Chat I/O for account %d: %w", accountID, err)
	}
	return nil
}

func chatmailAccountQR(relay string) (string, error) {
	relay = strings.TrimSpace(relay)
	if relay == "" {
		relay = DefaultChatmailRelay
	}
	if strings.HasPrefix(strings.ToUpper(relay), "DCACCOUNT:") {
		return relay, nil
	}
	if !strings.Contains(relay, "://") {
		relay = "https://" + relay + "/new"
	}
	u, err := url.Parse(relay)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("invalid chatmail relay %q", relay)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/new"
	}
	return "DCACCOUNT:" + u.String(), nil
}
