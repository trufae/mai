package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"deltachatmcp/lib"
	"mcplib"
)

type mcpTool struct {
	definition mcplib.ToolDefinition
	handler    mcplib.ToolHandlerWithContext
}

type deltaChatService struct {
	client            *deltachat.Client
	accountManagement bool
}

func newDeltaChatService(client *deltachat.Client, accountManagement bool) *deltaChatService {
	return &deltaChatService{client: client, accountManagement: accountManagement}
}

func (s *deltaChatService) tools() []mcpTool {
	tools := []mcpTool{
		s.tool("search_contacts", "Search known Delta Chat contacts. With no query, lists recent known contacts.", map[string]any{
			"query":      stringProperty("Optional contact name or email filter."),
			"account_id": accountIDProperty(),
			"limit":      limitProperty(50),
		}, nil, s.searchContacts),
		s.tool("list_conversations", "List recent Delta Chat conversations, optionally filtered by name.", map[string]any{
			"query":      stringProperty("Optional conversation-name filter."),
			"account_id": accountIDProperty(),
			"limit":      limitProperty(50),
		}, nil, s.listConversations),
		s.tool("read_conversation", "Read the most recent messages in a Delta Chat conversation, oldest first.", map[string]any{
			"chat_id":    uintProperty("Conversation ID."),
			"account_id": accountIDProperty(),
			"limit":      limitProperty(50),
		}, []string{"chat_id"}, s.readConversation),
		s.tool("receive_pending_messages", "Receive messages waiting for the active account. By default accepts contact requests and marks returned messages seen so they are consumed.", map[string]any{
			"account_id":      accountIDProperty(),
			"limit":           limitProperty(0),
			"mark_seen":       boolProperty("Mark returned messages seen and consume them. Default: true."),
			"accept_requests": boolProperty("Accept returned contact-request chats before marking seen. Default: true."),
		}, nil, s.receivePendingMessages),
		s.tool("send_message", "Send a text message to a conversation ID, email address, exact contact name, or exact conversation name.", map[string]any{
			"recipient":         stringProperty("Conversation ID, email address, contact name, or conversation name."),
			"message":           stringProperty("Message text."),
			"account_id":        accountIDProperty(),
			"quoted_message_id": uintProperty("Optional message ID to quote."),
		}, []string{"recipient", "message"}, s.sendMessage),
		s.tool("send_attachment", "Attach a local file to a Delta Chat message, with an optional caption and filename.", map[string]any{
			"recipient":         stringProperty("Conversation ID, email address, contact name, or conversation name."),
			"file_path":         stringProperty("Path to a local regular file."),
			"caption":           stringProperty("Optional attachment caption."),
			"filename":          stringProperty("Optional filename shown to recipients."),
			"account_id":        accountIDProperty(),
			"quoted_message_id": uintProperty("Optional message ID to quote."),
		}, []string{"recipient", "file_path"}, s.sendAttachment),
		s.tool("accept_chat", "Accept a Delta Chat contact-request conversation.", map[string]any{
			"chat_id":    uintProperty("Conversation ID."),
			"account_id": accountIDProperty(),
		}, []string{"chat_id"}, s.acceptChat),
		s.tool("clear_chat", "Permanently clear a Delta Chat conversation from this device and schedule its messages for server deletion.", map[string]any{
			"chat_id":    uintProperty("Conversation ID to clear."),
			"confirm":    boolProperty("Must be true to confirm permanent deletion."),
			"account_id": accountIDProperty(),
		}, []string{"chat_id", "confirm"}, s.clearChat),
		s.tool("get_invite_link", "Get a secure Delta Chat invite link for the active account or a group conversation.", map[string]any{
			"chat_id":    uintProperty("Optional group conversation ID. Omit for an account contact invite."),
			"account_id": accountIDProperty(),
		}, nil, s.getInviteLink),
		s.tool("join_invite", "Join a Delta Chat contact or group using a secure invite link.", map[string]any{
			"invite_link": stringProperty("Delta Chat secure-join link."),
			"account_id":  accountIDProperty(),
		}, []string{"invite_link"}, s.joinInvite),
	}

	if !s.accountManagement {
		return tools
	}
	return append(tools,
		s.tool("list_accounts", "List Delta Chat accounts and show which account is active.", map[string]any{}, nil, s.listAccounts),
		s.tool("create_account", "Create and select a Delta Chat account. With no arguments, creates a chatmail account on nine.testrun.org. Provide email and password together for a conventional email account.", map[string]any{
			"relay":         stringProperty("Optional chatmail relay domain or HTTPS account URL."),
			"email":         stringProperty("Conventional email address; requires password."),
			"password":      stringProperty("Conventional email password; requires email."),
			"display_name":  stringProperty("Optional profile display name."),
			"profile_image": stringProperty("Optional local profile image path."),
			"imap_server":   stringProperty("Optional IMAP server override."),
			"imap_port":     portProperty("Optional IMAP port."),
			"imap_security": securityProperty("Optional IMAP socket security."),
			"imap_user":     stringProperty("Optional IMAP username."),
			"smtp_server":   stringProperty("Optional SMTP server override."),
			"smtp_port":     portProperty("Optional SMTP port."),
			"smtp_security": securityProperty("Optional SMTP socket security."),
			"smtp_user":     stringProperty("Optional SMTP username."),
			"smtp_password": stringProperty("Optional SMTP password when different from the email password."),
		}, nil, s.createAccount),
		s.tool("switch_account", "Select the account used when messaging tools omit account_id.", map[string]any{
			"account_id": uintProperty("Account ID to select."),
		}, []string{"account_id"}, s.switchAccount),
		s.tool("set_account_profile", "Update the selected account's display name and/or profile image.", map[string]any{
			"account_id":    accountIDProperty(),
			"display_name":  stringProperty("New non-empty display name."),
			"profile_image": stringProperty("Path to a new local profile image."),
		}, nil, s.setAccountProfile),
		s.tool("remove_account", "Permanently remove a Delta Chat account and its local data.", map[string]any{
			"account_id": uintProperty("Account ID to permanently remove."),
			"confirm":    boolProperty("Must be true to confirm permanent deletion."),
		}, []string{"account_id", "confirm"}, s.removeAccount),
	)
}

func (s *deltaChatService) tool(name, description string, properties map[string]any, required []string, handler mcplib.ToolHandlerWithContext) mcpTool {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) != 0 {
		schema["required"] = required
	}
	return mcpTool{
		definition: mcplib.ToolDefinition{Name: name, Description: description, InputSchema: schema},
		handler:    handler,
	}
}

func (s *deltaChatService) searchContacts(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	limit, err := intArgument(args, "limit", 50, 500)
	if err != nil {
		return nil, err
	}
	contacts, err := s.client.Contacts(ctx, accountID, stringArgument(args, "query"), limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"account_id": accountID, "contacts": contacts, "count": len(contacts)}, nil
}

func (s *deltaChatService) listConversations(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	limit, err := intArgument(args, "limit", 50, 500)
	if err != nil {
		return nil, err
	}
	chats, err := s.client.Chats(ctx, accountID, stringArgument(args, "query"), limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"account_id": accountID, "conversations": chats, "count": len(chats)}, nil
}

func (s *deltaChatService) readConversation(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	chatID, err := requiredUint32(args, "chat_id")
	if err != nil {
		return nil, err
	}
	limit, err := intArgument(args, "limit", 50, 500)
	if err != nil {
		return nil, err
	}
	chat, err := s.client.Chat(ctx, accountID, chatID)
	if err != nil {
		return nil, err
	}
	messages, err := s.client.Conversation(ctx, accountID, chatID, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"account_id": accountID, "conversation": chat, "messages": messages, "count": len(messages)}, nil
}

func (s *deltaChatService) receivePendingMessages(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	if err := s.client.StartIO(ctx, accountID); err != nil {
		return nil, err
	}
	limit, err := intArgument(args, "limit", 0, 500)
	if err != nil {
		return nil, err
	}
	ids, err := s.client.PendingMessageIDs(ctx, accountID)
	if err != nil {
		return nil, err
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	messages, err := s.client.Messages(ctx, accountID, ids)
	if err != nil {
		return nil, err
	}

	chats := make([]deltachat.Chat, 0)
	seenChats := make(map[uint32]bool)
	for _, message := range messages {
		if message.LoadError != "" || seenChats[message.ChatID] {
			continue
		}
		chat, err := s.client.Chat(ctx, accountID, message.ChatID)
		if err != nil {
			return nil, err
		}
		seenChats[message.ChatID] = true
		chats = append(chats, chat)
	}

	markSeen, err := boolArgument(args, "mark_seen", true)
	if err != nil {
		return nil, err
	}
	acceptRequests, err := boolArgument(args, "accept_requests", true)
	if err != nil {
		return nil, err
	}
	markedSeenIDs := []uint32{}
	if markSeen {
		markedSeenIDs = consumableMessageIDs(ids, messages)
		consumableChats := make(map[uint32]bool)
		for i := range markedSeenIDs {
			consumableChats[messages[i].ChatID] = true
		}
		if acceptRequests {
			for _, chat := range chats {
				if chat.IsContactRequest && consumableChats[chat.ID] {
					if err := s.client.AcceptChat(ctx, accountID, chat.ID); err != nil {
						return nil, err
					}
				}
			}
		}
		if err := s.client.MarkSeen(ctx, accountID, markedSeenIDs); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"account_id":      accountID,
		"messages":        messages,
		"conversations":   chats,
		"count":           len(messages),
		"marked_seen":     markSeen,
		"marked_seen_ids": markedSeenIDs,
	}, nil
}

func (s *deltaChatService) sendMessage(ctx context.Context, args map[string]any) (any, error) {
	message, err := requiredString(args, "message")
	if err != nil {
		return nil, err
	}
	return s.send(ctx, args, message, "", "")
}

func (s *deltaChatService) sendAttachment(ctx context.Context, args map[string]any) (any, error) {
	filePath, err := requiredString(args, "file_path")
	if err != nil {
		return nil, err
	}
	return s.send(ctx, args, stringArgument(args, "caption"), filePath, stringArgument(args, "filename"))
}

func (s *deltaChatService) send(ctx context.Context, args map[string]any, message, filePath, filename string) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	recipient, err := requiredString(args, "recipient")
	if err != nil {
		return nil, err
	}
	quotedMessageID, err := optionalUint32(args, "quoted_message_id")
	if err != nil {
		return nil, err
	}
	if err := s.client.StartIO(ctx, accountID); err != nil {
		return nil, err
	}
	chatID, err := s.client.ResolveChat(ctx, accountID, recipient)
	if err != nil {
		return nil, err
	}
	return s.client.Send(ctx, accountID, chatID, message, filePath, filename, quotedMessageID)
}

func (s *deltaChatService) acceptChat(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	chatID, err := requiredUint32(args, "chat_id")
	if err != nil {
		return nil, err
	}
	if err := s.client.AcceptChat(ctx, accountID, chatID); err != nil {
		return nil, err
	}
	return map[string]any{"account_id": accountID, "chat_id": chatID, "accepted": true}, nil
}

func (s *deltaChatService) clearChat(ctx context.Context, args map[string]any) (any, error) {
	confirmed, err := boolArgument(args, "confirm", false)
	if err != nil || !confirmed {
		return nil, fmt.Errorf("confirm must be true to clear a chat permanently")
	}
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	chatID, err := requiredUint32(args, "chat_id")
	if err != nil {
		return nil, err
	}
	if err := s.client.DeleteChat(ctx, accountID, chatID); err != nil {
		return nil, err
	}
	return map[string]any{"account_id": accountID, "chat_id": chatID, "cleared": true}, nil
}

func (s *deltaChatService) getInviteLink(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	chatID, err := optionalUint32(args, "chat_id")
	if err != nil {
		return nil, err
	}
	invite, err := s.client.InviteLink(ctx, accountID, chatID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"account_id": accountID, "chat_id": chatID, "invite_link": invite}, nil
}

func (s *deltaChatService) joinInvite(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	invite, err := requiredString(args, "invite_link")
	if err != nil {
		return nil, err
	}
	if err := s.client.StartIO(ctx, accountID); err != nil {
		return nil, err
	}
	chatID, err := s.client.JoinInvite(ctx, accountID, invite)
	if err != nil {
		return nil, err
	}
	return map[string]any{"account_id": accountID, "chat_id": chatID, "joined": true}, nil
}

func (s *deltaChatService) listAccounts(ctx context.Context, args map[string]any) (any, error) {
	accounts, err := s.client.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	selected, err := s.client.SelectedAccountID(ctx)
	if err != nil {
		return nil, err
	}
	path := s.client.AccountsPath()
	if path == "" {
		path = "accounts"
	}
	return map[string]any{
		"accounts":            accounts,
		"count":               len(accounts),
		"selected_account_id": selected,
		"accounts_path":       path,
	}, nil
}

func (s *deltaChatService) createAccount(ctx context.Context, args map[string]any) (any, error) {
	opts := deltachat.CreateAccountOptions{
		Relay:        stringArgument(args, "relay"),
		DisplayName:  stringArgument(args, "display_name"),
		ProfileImage: stringArgument(args, "profile_image"),
	}
	email := stringArgument(args, "email")
	password := stringArgument(args, "password")
	if email != "" || password != "" {
		imapPort, err := uint16Argument(args, "imap_port")
		if err != nil {
			return nil, err
		}
		smtpPort, err := uint16Argument(args, "smtp_port")
		if err != nil {
			return nil, err
		}
		opts.Login = &deltachat.LoginParams{
			Address:      email,
			Password:     password,
			IMAPServer:   stringArgument(args, "imap_server"),
			IMAPPort:     imapPort,
			IMAPSecurity: stringArgument(args, "imap_security"),
			IMAPUser:     stringArgument(args, "imap_user"),
			SMTPServer:   stringArgument(args, "smtp_server"),
			SMTPPort:     smtpPort,
			SMTPSecurity: stringArgument(args, "smtp_security"),
			SMTPUser:     stringArgument(args, "smtp_user"),
			SMTPPassword: stringArgument(args, "smtp_password"),
		}
	}
	account, err := s.client.CreateAccount(ctx, opts)
	if err != nil {
		return nil, err
	}
	invite, _ := s.client.InviteLink(ctx, account.ID, 0)
	return map[string]any{"account": account, "selected": true, "invite_link": invite}, nil
}

func (s *deltaChatService) switchAccount(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := requiredUint32(args, "account_id")
	if err != nil {
		return nil, err
	}
	account, err := s.client.Account(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if err := s.client.SelectAccount(ctx, accountID); err != nil {
		return nil, err
	}
	return map[string]any{"account": account, "selected": true}, nil
}

func (s *deltaChatService) setAccountProfile(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.account(ctx, args, false)
	if err != nil {
		return nil, err
	}
	displayName := stringArgument(args, "display_name")
	profileImage := stringArgument(args, "profile_image")
	if displayName == "" && profileImage == "" {
		return nil, fmt.Errorf("display_name or profile_image is required")
	}
	if err := s.client.SetProfile(ctx, accountID, displayName, profileImage); err != nil {
		return nil, err
	}
	account, err := s.client.Account(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"account": account, "updated": true}, nil
}

func (s *deltaChatService) removeAccount(ctx context.Context, args map[string]any) (any, error) {
	confirmed, err := boolArgument(args, "confirm", false)
	if err != nil || !confirmed {
		return nil, fmt.Errorf("confirm must be true to remove an account permanently")
	}
	accountID, err := requiredUint32(args, "account_id")
	if err != nil {
		return nil, err
	}
	if _, err := s.client.Account(ctx, accountID); err != nil {
		return nil, err
	}
	if err := s.client.RemoveAccount(ctx, accountID); err != nil {
		return nil, err
	}
	return map[string]any{"account_id": accountID, "removed": true}, nil
}

func (s *deltaChatService) configuredAccount(ctx context.Context, args map[string]any) (uint32, error) {
	return s.account(ctx, args, true)
}

func (s *deltaChatService) account(ctx context.Context, args map[string]any, configured bool) (uint32, error) {
	requested, err := optionalUint32(args, "account_id")
	if err != nil {
		return 0, err
	}
	return s.client.ResolveAccount(ctx, requested, configured)
}

func stringProperty(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func uintProperty(description string) map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "description": description}
}

func accountIDProperty() map[string]any {
	return uintProperty("Optional account ID. Omit to use the selected account, or the first configured account.")
}

func boolProperty(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func limitProperty(defaultValue int) map[string]any {
	description := "Maximum number of results."
	minimum := 0
	if defaultValue > 0 {
		description = fmt.Sprintf("Maximum number of results. Default: %d.", defaultValue)
		minimum = 1
	} else {
		description += " Omit or use 0 for all pending results."
	}
	return map[string]any{"type": "integer", "minimum": minimum, "maximum": 500, "description": description}
}

func portProperty(description string) map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "maximum": 65535, "description": description}
}

func securityProperty(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"enum":        []string{"Automatic", "Ssl", "Starttls", "Plain"},
		"description": description,
	}
}

func requiredString(args map[string]any, name string) (string, error) {
	value := stringArgument(args, name)
	if value == "" {
		return "", fmt.Errorf("%s is required and must be a non-empty string", name)
	}
	return value, nil
}

func stringArgument(args map[string]any, name string) string {
	value, _ := args[name].(string)
	return strings.TrimSpace(value)
}

func requiredUint32(args map[string]any, name string) (uint32, error) {
	value, err := optionalUint32(args, name)
	if err != nil {
		return 0, err
	}
	if value == 0 {
		return 0, fmt.Errorf("%s is required and must be a positive integer", name)
	}
	return value, nil
}

func optionalUint32(args map[string]any, name string) (uint32, error) {
	raw, ok := args[name]
	if !ok || raw == nil {
		return 0, nil
	}
	value, ok := raw.(float64)
	if !ok || value <= 0 || value > math.MaxUint32 || math.Trunc(value) != value {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return uint32(value), nil
}

func uint16Argument(args map[string]any, name string) (uint16, error) {
	value, err := optionalUint32(args, name)
	if err != nil {
		return 0, err
	}
	if value > math.MaxUint16 {
		return 0, fmt.Errorf("%s must be between 1 and 65535", name)
	}
	return uint16(value), nil
}

func intArgument(args map[string]any, name string, defaultValue, maximum int) (int, error) {
	raw, ok := args[name]
	if !ok || raw == nil {
		return defaultValue, nil
	}
	value, ok := raw.(float64)
	if !ok || value < 0 || value > float64(maximum) || math.Trunc(value) != value {
		return 0, fmt.Errorf("%s must be an integer between 0 and %d", name, maximum)
	}
	if value == 0 && defaultValue > 0 {
		return defaultValue, nil
	}
	return int(value), nil
}

func consumableMessageIDs(ids []uint32, messages []deltachat.Message) []uint32 {
	consumable := make([]uint32, 0, len(ids))
	for i, id := range ids {
		if i >= len(messages) || messages[i].LoadError != "" {
			break
		}
		consumable = append(consumable, id)
	}
	return consumable
}

func boolArgument(args map[string]any, name string, defaultValue bool) (bool, error) {
	raw, ok := args[name]
	if !ok || raw == nil {
		return defaultValue, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return value, nil
}
