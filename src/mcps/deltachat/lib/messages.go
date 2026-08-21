package deltachat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/mail"
	"path/filepath"
	"strconv"
	"strings"
)

// Contacts searches known, unblocked contacts. An empty query lists them.
func (c *Client) Contacts(ctx context.Context, accountID uint32, query string, limit int) ([]Contact, error) {
	var queryArg any
	if query = strings.TrimSpace(query); query != "" {
		queryArg = query
	}
	var contacts []Contact
	if err := c.Call(ctx, "get_contacts", &contacts, accountID, uint32(0), queryArg); err != nil {
		return nil, fmt.Errorf("search Delta Chat contacts: %w", err)
	}
	if limit > 0 && len(contacts) > limit {
		contacts = contacts[:limit]
	}
	return contacts, nil
}

// Contact returns one known contact.
func (c *Client) Contact(ctx context.Context, accountID, contactID uint32) (Contact, error) {
	var contact Contact
	if err := c.Call(ctx, "get_contact", &contact, accountID, contactID); err != nil {
		return Contact{}, fmt.Errorf("get Delta Chat contact %d: %w", contactID, err)
	}
	return contact, nil
}

// ContactsByIDs returns contacts in the same order as the supplied IDs.
func (c *Client) ContactsByIDs(ctx context.Context, accountID uint32, ids []uint32) ([]Contact, error) {
	if len(ids) == 0 {
		return []Contact{}, nil
	}
	var records map[string]Contact
	if err := c.Call(ctx, "get_contacts_by_ids", &records, accountID, ids); err != nil {
		return nil, fmt.Errorf("get Delta Chat contacts: %w", err)
	}
	contacts := make([]Contact, 0, len(ids))
	for _, id := range ids {
		contact, ok := records[strconv.FormatUint(uint64(id), 10)]
		if !ok {
			return nil, fmt.Errorf("Delta Chat contact %d was missing from the RPC response", id)
		}
		contacts = append(contacts, contact)
	}
	return contacts, nil
}

// ContactEncryptionInfo returns human-readable fingerprint and verification
// information for a contact.
func (c *Client) ContactEncryptionInfo(ctx context.Context, accountID, contactID uint32) (string, error) {
	var info string
	if err := c.Call(ctx, "get_contact_encryption_info", &info, accountID, contactID); err != nil {
		return "", fmt.Errorf("get Delta Chat contact %d encryption info: %w", contactID, err)
	}
	return info, nil
}

// Chats returns recent conversations, optionally filtered by a query.
func (c *Client) Chats(ctx context.Context, accountID uint32, query string, limit int) ([]Chat, error) {
	var queryArg any
	if query = strings.TrimSpace(query); query != "" {
		queryArg = query
	}
	var ids []uint32
	if err := c.Call(ctx, "get_chatlist_entries", &ids, accountID, nil, queryArg, nil); err != nil {
		return nil, fmt.Errorf("list Delta Chat conversations: %w", err)
	}
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}

	chats := make([]Chat, 0, len(ids))
	for _, id := range ids {
		chat, err := c.Chat(ctx, accountID, id)
		if err != nil {
			return nil, err
		}
		chats = append(chats, chat)
	}
	return chats, nil
}

// Chat gets one conversation.
func (c *Client) Chat(ctx context.Context, accountID, chatID uint32) (Chat, error) {
	var chat Chat
	if err := c.Call(ctx, "get_full_chat_by_id", &chat, accountID, chatID); err != nil {
		return Chat{}, fmt.Errorf("get Delta Chat conversation %d: %w", chatID, err)
	}
	return chat, nil
}

// Messages returns messages by ID in the same order as ids.
func (c *Client) Messages(ctx context.Context, accountID uint32, ids []uint32) ([]Message, error) {
	if len(ids) == 0 {
		return []Message{}, nil
	}
	var records map[string]json.RawMessage
	if err := c.Call(ctx, "get_messages", &records, accountID, ids); err != nil {
		return nil, fmt.Errorf("get Delta Chat messages: %w", err)
	}

	messages := make([]Message, 0, len(ids))
	for _, id := range ids {
		raw, ok := records[strconv.FormatUint(uint64(id), 10)]
		if !ok {
			messages = append(messages, Message{ID: id, LoadError: "message missing from RPC response"})
			continue
		}
		var envelope struct {
			Kind  string `json:"kind"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("decode Delta Chat message %d: %w", id, err)
		}
		if strings.EqualFold(envelope.Kind, "loadingError") {
			messages = append(messages, Message{ID: id, LoadError: envelope.Error})
			continue
		}
		var message Message
		if err := json.Unmarshal(raw, &message); err != nil {
			return nil, fmt.Errorf("decode Delta Chat message %d: %w", id, err)
		}
		messages = append(messages, message)
	}
	return messages, nil
}

// Message returns one message by ID.
func (c *Client) Message(ctx context.Context, accountID, messageID uint32) (Message, error) {
	var message *Message
	if err := c.Call(ctx, "get_message", &message, accountID, messageID); err != nil {
		return Message{}, fmt.Errorf("get Delta Chat message %d: %w", messageID, err)
	}
	if message == nil {
		return Message{}, fmt.Errorf("Delta Chat message %d was not found", messageID)
	}
	return *message, nil
}

// SearchMessages finds messages containing query, optionally within one chat.
func (c *Client) SearchMessages(ctx context.Context, accountID uint32, query string, chatID uint32, limit int) ([]Message, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("Delta Chat message search query is required")
	}
	var chatArg any
	if chatID != 0 {
		chatArg = chatID
	}
	var ids []uint32
	if err := c.Call(ctx, "search_messages", &ids, accountID, query, chatArg); err != nil {
		return nil, fmt.Errorf("search Delta Chat messages: %w", err)
	}
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	return c.Messages(ctx, accountID, ids)
}

// EditMessage replaces the text of an editable outgoing message.
func (c *Client) EditMessage(ctx context.Context, accountID, messageID uint32, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("Delta Chat edited message text is required")
	}
	if err := c.Call(ctx, "send_edit_request", nil, accountID, messageID, text); err != nil {
		return fmt.Errorf("edit Delta Chat message %d: %w", messageID, err)
	}
	return nil
}

// DeleteMessages deletes messages locally and from the IMAP server. When
// forAll is true it also requests deletion for all chat members.
func (c *Client) DeleteMessages(ctx context.Context, accountID uint32, messageIDs []uint32, forAll bool) error {
	messageIDs, err := normalizeMessageIDs(messageIDs)
	if err != nil {
		return err
	}
	method := "delete_messages"
	if forAll {
		method = "delete_messages_for_all"
	}
	if err := c.Call(ctx, method, nil, accountID, messageIDs); err != nil {
		return fmt.Errorf("delete Delta Chat messages: %w", err)
	}
	return nil
}

// ForwardMessages forwards messages to another chat.
func (c *Client) ForwardMessages(ctx context.Context, accountID uint32, messageIDs []uint32, chatID uint32) error {
	messageIDs, err := normalizeMessageIDs(messageIDs)
	if err != nil {
		return err
	}
	if chatID == 0 {
		return fmt.Errorf("Delta Chat destination conversation is required")
	}
	if err := c.Call(ctx, "forward_messages", nil, accountID, messageIDs, chatID); err != nil {
		return fmt.Errorf("forward Delta Chat messages: %w", err)
	}
	return nil
}

// DownloadMessage requests asynchronous download of a partially downloaded
// message. Message changes are applied by the RPC server in the background.
func (c *Client) DownloadMessage(ctx context.Context, accountID, messageID uint32) error {
	if err := c.Call(ctx, "download_full_message", nil, accountID, messageID); err != nil {
		return fmt.Errorf("download Delta Chat message %d: %w", messageID, err)
	}
	return nil
}

// MessageInformation returns structured transport and expiry details.
func (c *Client) MessageInformation(ctx context.Context, accountID, messageID uint32) (MessageInfo, error) {
	var info MessageInfo
	if err := c.Call(ctx, "get_message_info_object", &info, accountID, messageID); err != nil {
		return MessageInfo{}, fmt.Errorf("get Delta Chat message %d info: %w", messageID, err)
	}
	return info, nil
}

// RawMessageInformation returns the extended human-readable message details.
func (c *Client) RawMessageInformation(ctx context.Context, accountID, messageID uint32) (string, error) {
	var info string
	if err := c.Call(ctx, "get_message_info", &info, accountID, messageID); err != nil {
		return "", fmt.Errorf("get Delta Chat message %d raw info: %w", messageID, err)
	}
	return info, nil
}

// MessageReadReceipts returns individual read receipts and the core's total
// receipt count. The count is useful for broadcast-channel view counts.
func (c *Client) MessageReadReceipts(ctx context.Context, accountID, messageID uint32) ([]MessageReadReceipt, uint, error) {
	var receipts []MessageReadReceipt
	if err := c.Call(ctx, "get_message_read_receipts", &receipts, accountID, messageID); err != nil {
		return nil, 0, fmt.Errorf("get Delta Chat message %d read receipts: %w", messageID, err)
	}
	var count uint
	if err := c.Call(ctx, "get_message_read_receipt_count", &count, accountID, messageID); err != nil {
		return nil, 0, fmt.Errorf("get Delta Chat message %d read receipt count: %w", messageID, err)
	}
	return receipts, count, nil
}

// Conversation returns up to limit most recent messages, oldest first.
func (c *Client) Conversation(ctx context.Context, accountID, chatID uint32, limit int) ([]Message, error) {
	var ids []uint32
	if err := c.Call(ctx, "get_message_ids", &ids, accountID, chatID, false, false); err != nil {
		return nil, fmt.Errorf("read Delta Chat conversation %d: %w", chatID, err)
	}
	if limit > 0 && len(ids) > limit {
		ids = ids[len(ids)-limit:]
	}
	return c.Messages(ctx, accountID, ids)
}

// PendingMessageIDs returns messages waiting to be consumed by a bot.
func (c *Client) PendingMessageIDs(ctx context.Context, accountID uint32) ([]uint32, error) {
	var ids []uint32
	if err := c.Call(ctx, "get_next_msgs", &ids, accountID); err != nil {
		return nil, fmt.Errorf("receive pending Delta Chat messages: %w", err)
	}
	return ids, nil
}

// MarkSeen consumes pending messages and sends read receipts where applicable.
func (c *Client) MarkSeen(ctx context.Context, accountID uint32, messageIDs []uint32) error {
	if len(messageIDs) == 0 {
		return nil
	}
	if err := c.Call(ctx, "markseen_msgs", nil, accountID, messageIDs); err != nil {
		return fmt.Errorf("mark Delta Chat messages seen: %w", err)
	}
	return nil
}

// AcceptChat accepts a contact request conversation.
func (c *Client) AcceptChat(ctx context.Context, accountID, chatID uint32) error {
	if err := c.Call(ctx, "accept_chat", nil, accountID, chatID); err != nil {
		return fmt.Errorf("accept Delta Chat conversation %d: %w", chatID, err)
	}
	return nil
}

// DeleteChat clears a conversation from the device and schedules its messages
// for deletion from the server.
func (c *Client) DeleteChat(ctx context.Context, accountID, chatID uint32) error {
	if err := c.Call(ctx, "delete_chat", nil, accountID, chatID); err != nil {
		return fmt.Errorf("clear Delta Chat conversation %d: %w", chatID, err)
	}
	return nil
}

// ResolveChat resolves a chat ID, email address, exact contact name, or exact
// conversation name. Unknown email addresses are added as contacts.
func (c *Client) ResolveChat(ctx context.Context, accountID uint32, target string) (uint32, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return 0, fmt.Errorf("Delta Chat recipient is required")
	}
	if id, ok := parseChatID(target); ok {
		return id, nil
	}
	if address, ok := emailAddress(target); ok {
		return c.resolveEmailChat(ctx, accountID, address)
	}

	contacts, err := c.Contacts(ctx, accountID, target, 0)
	if err != nil {
		return 0, err
	}
	contacts = exactContacts(target, contacts)
	if len(contacts) == 1 {
		return c.chatForContact(ctx, accountID, contacts[0].ID)
	}
	if len(contacts) > 1 {
		return 0, fmt.Errorf("ambiguous Delta Chat contact %q", target)
	}

	chats, err := c.Chats(ctx, accountID, target, 0)
	if err != nil {
		return 0, err
	}
	var exact []Chat
	for _, chat := range chats {
		if strings.EqualFold(strings.TrimSpace(chat.Name), target) {
			exact = append(exact, chat)
		}
	}
	if len(exact) == 1 {
		return exact[0].ID, nil
	}
	if len(exact) > 1 {
		return 0, fmt.Errorf("ambiguous Delta Chat conversation %q", target)
	}
	if len(chats) == 1 {
		return chats[0].ID, nil
	}
	return 0, fmt.Errorf("Delta Chat recipient %q was not found", target)
}

// Send queues text and/or a file for delivery.
func (c *Client) Send(ctx context.Context, accountID, chatID uint32, text, filePath, filename string, quotedMessageID uint32) (SentMessage, error) {
	text = strings.TrimSpace(text)
	filePath = strings.TrimSpace(filePath)
	if text == "" && filePath == "" {
		return SentMessage{}, fmt.Errorf("Delta Chat message text or file is required")
	}

	var textArg, fileArg, filenameArg, quoteArg any
	if text != "" {
		textArg = text
	}
	if filePath != "" {
		abs, err := regularFilePath(filePath)
		if err != nil {
			return SentMessage{}, fmt.Errorf("invalid Delta Chat attachment: %w", err)
		}
		fileArg = abs
		if filename = strings.TrimSpace(filename); filename != "" {
			filenameArg = filepath.Base(filename)
		}
	}
	if quotedMessageID != 0 {
		quoteArg = quotedMessageID
	}

	var result []json.RawMessage
	if err := c.Call(ctx, "misc_send_msg", &result,
		accountID, chatID, textArg, fileArg, filenameArg, nil, quoteArg); err != nil {
		return SentMessage{}, fmt.Errorf("send Delta Chat message: %w", err)
	}
	if len(result) != 2 {
		return SentMessage{}, fmt.Errorf("send Delta Chat message: invalid RPC response")
	}
	var sent SentMessage
	sent.AccountID = accountID
	sent.ChatID = chatID
	if err := json.Unmarshal(result[0], &sent.MessageID); err != nil {
		return SentMessage{}, fmt.Errorf("decode sent Delta Chat message ID: %w", err)
	}
	if err := json.Unmarshal(result[1], &sent.Message); err != nil {
		return SentMessage{}, fmt.Errorf("decode sent Delta Chat message: %w", err)
	}
	return sent, nil
}

// Reply queues text and/or a file as a reply to an existing message. The
// destination conversation is derived from the original message.
func (c *Client) Reply(ctx context.Context, accountID, messageID uint32, text, filePath, filename string) (SentMessage, error) {
	original, err := c.Message(ctx, accountID, messageID)
	if err != nil {
		return SentMessage{}, err
	}
	return c.Send(ctx, accountID, original.ChatID, text, filePath, filename, messageID)
}

// InviteLink returns a secure-join link for the account or a group chat.
func (c *Client) InviteLink(ctx context.Context, accountID, chatID uint32) (string, error) {
	var chatArg any
	if chatID != 0 {
		chatArg = chatID
	}
	var invite string
	if err := c.Call(ctx, "get_chat_securejoin_qr_code", &invite, accountID, chatArg); err != nil {
		return "", fmt.Errorf("get Delta Chat invite link: %w", err)
	}
	return invite, nil
}

// JoinInvite joins a contact or group through a secure-join link.
func (c *Client) JoinInvite(ctx context.Context, accountID uint32, invite string) (uint32, error) {
	invite = strings.TrimSpace(invite)
	if invite == "" {
		return 0, fmt.Errorf("Delta Chat invite link is required")
	}
	var chatID uint32
	if err := c.Call(ctx, "secure_join", &chatID, accountID, invite); err != nil {
		return 0, fmt.Errorf("join Delta Chat invite: %w", err)
	}
	if chatID != 0 {
		_ = c.AcceptChat(ctx, accountID, chatID)
	}
	return chatID, nil
}

// SendReaction replaces this account's reaction to a message. An empty slice
// clears the current reaction.
func (c *Client) SendReaction(ctx context.Context, accountID, messageID uint32, reactions []string) (uint32, error) {
	var reactionMessageID uint32
	if err := c.Call(ctx, "send_reaction", &reactionMessageID, accountID, messageID, reactions); err != nil {
		return 0, fmt.Errorf("react to Delta Chat message %d: %w", messageID, err)
	}
	return reactionMessageID, nil
}

// MessageReactions returns all reactions to a message. A nil result means the
// message has no reactions.
func (c *Client) MessageReactions(ctx context.Context, accountID, messageID uint32) (*Reactions, error) {
	var reactions *Reactions
	if err := c.Call(ctx, "get_message_reactions", &reactions, accountID, messageID); err != nil {
		return nil, fmt.Errorf("get reactions for Delta Chat message %d: %w", messageID, err)
	}
	return reactions, nil
}

func (c *Client) resolveEmailChat(ctx context.Context, accountID uint32, address string) (uint32, error) {
	contactID, err := c.ResolveContact(ctx, accountID, address)
	if err != nil {
		return 0, err
	}
	return c.chatForContact(ctx, accountID, contactID)
}

// ResolveContact resolves a contact ID, email address, or exact contact name.
// Unknown email addresses are added to the contact list.
func (c *Client) ResolveContact(ctx context.Context, accountID uint32, target string) (uint32, error) {
	return c.resolveContact(ctx, accountID, target, true)
}

// LookupContact resolves an existing contact ID, email address, or exact name
// without creating a new contact.
func (c *Client) LookupContact(ctx context.Context, accountID uint32, target string) (uint32, error) {
	return c.resolveContact(ctx, accountID, target, false)
}

func (c *Client) resolveContact(ctx context.Context, accountID uint32, target string, create bool) (uint32, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return 0, fmt.Errorf("Delta Chat contact is required")
	}
	if id, ok := parseContactID(target); ok {
		return id, nil
	}
	if address, ok := emailAddress(target); ok {
		var contactID *uint32
		if err := c.Call(ctx, "lookup_contact_id_by_addr", &contactID, accountID, address); err != nil {
			return 0, fmt.Errorf("lookup Delta Chat contact %s: %w", address, err)
		}
		if contactID != nil && *contactID != 0 {
			return *contactID, nil
		}
		if !create {
			return 0, fmt.Errorf("Delta Chat contact %q was not found", target)
		}
		var created uint32
		if err := c.Call(ctx, "create_contact", &created, accountID, address, nil); err != nil {
			return 0, fmt.Errorf("create Delta Chat contact %s: %w", address, err)
		}
		return created, nil
	}
	contacts, err := c.Contacts(ctx, accountID, target, 0)
	if err != nil {
		return 0, err
	}
	contacts = exactContacts(target, contacts)
	if len(contacts) == 1 {
		return contacts[0].ID, nil
	}
	if len(contacts) > 1 {
		return 0, fmt.Errorf("ambiguous Delta Chat contact %q", target)
	}
	return 0, fmt.Errorf("Delta Chat contact %q was not found", target)
}

func normalizeMessageIDs(messageIDs []uint32) ([]uint32, error) {
	if len(messageIDs) == 0 {
		return nil, fmt.Errorf("at least one Delta Chat message ID is required")
	}
	result := make([]uint32, 0, len(messageIDs))
	seen := make(map[uint32]bool)
	for _, id := range messageIDs {
		if id == 0 {
			return nil, fmt.Errorf("Delta Chat message IDs must be positive")
		}
		if !seen[id] {
			result = append(result, id)
			seen[id] = true
		}
	}
	return result, nil
}

func (c *Client) chatForContact(ctx context.Context, accountID, contactID uint32) (uint32, error) {
	var chatID *uint32
	if err := c.Call(ctx, "get_chat_id_by_contact_id", &chatID, accountID, contactID); err != nil {
		return 0, fmt.Errorf("get Delta Chat conversation for contact %d: %w", contactID, err)
	}
	if chatID != nil && *chatID != 0 {
		return *chatID, nil
	}
	var created uint32
	if err := c.Call(ctx, "create_chat_by_contact_id", &created, accountID, contactID); err != nil {
		return 0, fmt.Errorf("create Delta Chat conversation for contact %d: %w", contactID, err)
	}
	return created, nil
}

func parseChatID(target string) (uint32, bool) {
	value := target
	if len(target) > 5 && strings.EqualFold(target[:5], "chat:") {
		value = strings.TrimSpace(target[5:])
	}
	id, err := strconv.ParseUint(value, 10, 32)
	return uint32(id), err == nil && id != 0
}

func parseContactID(target string) (uint32, bool) {
	value := target
	if len(target) > 8 && strings.EqualFold(target[:8], "contact:") {
		value = strings.TrimSpace(target[8:])
	}
	id, err := strconv.ParseUint(value, 10, 32)
	return uint32(id), err == nil && id != 0
}

func emailAddress(target string) (string, bool) {
	address, err := mail.ParseAddress(target)
	if err != nil || !strings.Contains(address.Address, "@") {
		return "", false
	}
	return strings.ToLower(address.Address), true
}

func exactContacts(query string, contacts []Contact) []Contact {
	var matches []Contact
	for _, contact := range contacts {
		aliases := []string{contact.Address, contact.Name, contact.DisplayName, contact.NameAndAddress}
		if local, _, ok := strings.Cut(contact.Address, "@"); ok {
			aliases = append(aliases, local, "@"+local)
		}
		for _, alias := range aliases {
			if strings.EqualFold(strings.TrimSpace(alias), query) {
				matches = append(matches, contact)
				break
			}
		}
	}
	if len(matches) == 0 && len(contacts) == 1 {
		return contacts
	}
	return matches
}
