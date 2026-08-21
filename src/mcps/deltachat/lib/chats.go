package deltachat

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// CreateGroup creates an encrypted group and adds the supplied contact IDs.
func (c *Client) CreateGroup(ctx context.Context, accountID uint32, name string, members []uint32) (Chat, error) {
	return c.createChat(ctx, accountID, "create_group_chat", name, members, false)
}

// CreateChannel creates an outgoing broadcast channel and adds the supplied
// recipient contact IDs.
func (c *Client) CreateChannel(ctx context.Context, accountID uint32, name string, members []uint32) (Chat, error) {
	return c.createChat(ctx, accountID, "create_broadcast", name, members)
}

func (c *Client) createChat(ctx context.Context, accountID uint32, method, name string, members []uint32, extra ...any) (Chat, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Chat{}, fmt.Errorf("Delta Chat conversation name is required")
	}
	params := []any{accountID, name}
	params = append(params, extra...)
	var chatID uint32
	if err := c.Call(ctx, method, &chatID, params...); err != nil {
		return Chat{}, fmt.Errorf("create Delta Chat conversation: %w", err)
	}
	for _, contactID := range members {
		if err := c.AddChatMember(ctx, accountID, chatID, contactID); err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = c.DeleteChat(cleanupCtx, accountID, chatID)
			cancel()
			return Chat{}, err
		}
	}
	return c.Chat(ctx, accountID, chatID)
}

// AddChatMember adds a contact to a group or broadcast channel.
func (c *Client) AddChatMember(ctx context.Context, accountID, chatID, contactID uint32) error {
	if err := c.Call(ctx, "add_contact_to_chat", nil, accountID, chatID, contactID); err != nil {
		return fmt.Errorf("add contact %d to Delta Chat conversation %d: %w", contactID, chatID, err)
	}
	return nil
}

// RemoveChatMember removes a contact from a group or broadcast channel.
func (c *Client) RemoveChatMember(ctx context.Context, accountID, chatID, contactID uint32) error {
	if err := c.Call(ctx, "remove_contact_from_chat", nil, accountID, chatID, contactID); err != nil {
		return fmt.Errorf("remove contact %d from Delta Chat conversation %d: %w", contactID, chatID, err)
	}
	return nil
}

// LeaveGroup removes this account from a group.
func (c *Client) LeaveGroup(ctx context.Context, accountID, chatID uint32) error {
	if err := c.Call(ctx, "leave_group", nil, accountID, chatID); err != nil {
		return fmt.Errorf("leave Delta Chat group %d: %w", chatID, err)
	}
	return nil
}

// SetChatName updates a group or channel name.
func (c *Client) SetChatName(ctx context.Context, accountID, chatID uint32, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("Delta Chat conversation name is required")
	}
	if err := c.Call(ctx, "set_chat_name", nil, accountID, chatID, name); err != nil {
		return fmt.Errorf("rename Delta Chat conversation %d: %w", chatID, err)
	}
	return nil
}

// SetChatDescription updates or clears a group or channel description.
func (c *Client) SetChatDescription(ctx context.Context, accountID, chatID uint32, description string) error {
	if err := c.Call(ctx, "set_chat_description", nil, accountID, chatID, description); err != nil {
		return fmt.Errorf("set Delta Chat conversation %d description: %w", chatID, err)
	}
	return nil
}

// SetChatProfileImage updates a group or channel image. An empty path clears it.
func (c *Client) SetChatProfileImage(ctx context.Context, accountID, chatID uint32, path string) error {
	var pathArg any
	if strings.TrimSpace(path) != "" {
		abs, err := regularFilePath(path)
		if err != nil {
			return fmt.Errorf("invalid Delta Chat conversation image: %w", err)
		}
		pathArg = abs
	}
	if err := c.Call(ctx, "set_chat_profile_image", nil, accountID, chatID, pathArg); err != nil {
		return fmt.Errorf("set Delta Chat conversation %d image: %w", chatID, err)
	}
	return nil
}
