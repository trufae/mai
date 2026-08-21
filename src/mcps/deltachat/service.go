package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

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
		s.tool("deltachat_contacts", "Contact operations. Actions: search and info. info requires contact, which may be a contact ID, email address, or exact name.", map[string]any{
			"action":     enumProperty("Contact operation.", "search", "info"),
			"contact":    stringProperty("Existing contact ID, email address, or exact name used by info."),
			"query":      stringProperty("Optional contact name or email filter."),
			"account_id": accountIDProperty(),
			"limit":      limitProperty(50),
		}, []string{"action"}, s.contactsTool),
		s.tool("deltachat_chats", "Chat, group, and channel operations. Actions: list, create, update, members, mute, invite, join, leave, accept, clear. Most mutations and members require chat_id; join requires invite_link.", map[string]any{
			"action":         enumProperty("Chat operation.", "list", "create", "update", "members", "mute", "invite", "join", "leave", "accept", "clear"),
			"account_id":     accountIDProperty(),
			"chat_id":        uintProperty("Conversation ID used by update, members, mute, invite, leave, accept, and clear."),
			"chat_type":      enumProperty("Conversation type for create.", "group", "channel"),
			"name":           stringProperty("Group or channel name for create/update."),
			"description":    stringProperty("Group or channel description for create/update; an empty value clears it."),
			"profile_image":  stringProperty("Local group/channel image path for create/update; an empty value clears it."),
			"members":        membersProperty("Initial group members or channel recipients."),
			"add_members":    membersProperty("Members or channel recipients to add during update."),
			"remove_members": membersProperty("Members or channel recipients to remove during update."),
			"invite_link":    stringProperty("Secure invite link used by join."),
			"query":          stringProperty("Optional conversation-name filter used by list."),
			"limit":          limitProperty(50),
			"muted":          boolProperty("Mute state required by mute. False unmutes the chat."),
			"mute_seconds":   boundedIntegerProperty("Optional mute duration in seconds. Omit or use 0 to mute forever.", 0, 31536000),
			"confirm":        boolProperty("Must be true for leave and clear."),
		}, []string{"action"}, s.chatsTool),
		s.tool("deltachat_messages", "Message operations. Actions: receive, read, search, send, reply, edit, delete, forward, download, react, reactions, receipts, info. search requires query; send/forward require recipient; single-message actions require message_id. Delete requires confirm=true. Downloads are asynchronous.", map[string]any{
			"action":          enumProperty("Message operation.", "receive", "read", "search", "send", "reply", "edit", "delete", "forward", "download", "react", "reactions", "receipts", "info"),
			"account_id":      accountIDProperty(),
			"chat_id":         uintProperty("Conversation ID used by read or to scope search."),
			"message_id":      uintProperty("Existing message used by single-message actions or as one delete/forward source."),
			"message_ids":     uintArrayProperty("Message IDs used by delete or forward. May be combined with message_id."),
			"recipient":       stringProperty("Conversation ID, email, exact contact name, or exact conversation name used by send or forward."),
			"query":           stringProperty("Required text query used by search."),
			"text":            stringProperty("Message, reply, or replacement edit text."),
			"file_path":       stringProperty("Optional local file to attach when sending or replying."),
			"filename":        stringProperty("Optional attachment filename shown to recipients."),
			"reactions":       stringArrayProperty("Reaction emoji. An empty array clears this account's reaction."),
			"limit":           scopedMessageLimitProperty(),
			"mark_seen":       boolProperty("For receive, mark returned messages seen and consume them. Default: true."),
			"accept_requests": boolProperty("For receive, accept contact-request chats before marking seen. Default: true."),
			"delete_for_all":  boolProperty("For delete, also request deletion for all chat members. Default: false."),
			"include_raw":     boolProperty("For info, include the extended human-readable message report. Default: false."),
			"confirm":         boolProperty("Must be true for delete."),
		}, []string{"action"}, s.messagesTool),
	}

	if !s.accountManagement {
		return tools
	}
	return append(tools,
		s.tool("deltachat_accounts", "Account operations. Actions: list, create, switch, update, remove. create with only the action uses nine.testrun.org; switch/remove require account_id.", map[string]any{
			"action":        enumProperty("Account operation.", "list", "create", "switch", "update", "remove"),
			"account_id":    accountIDProperty(),
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
			"confirm":       boolProperty("Must be true for remove."),
		}, []string{"action"}, s.accountsTool),
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

func (s *deltaChatService) contactsTool(ctx context.Context, args map[string]any) (any, error) {
	action, err := actionArgument(args, "search", "info")
	if err != nil {
		return nil, err
	}
	switch action {
	case "search":
		return s.searchContacts(ctx, args)
	case "info":
		return s.contactInfo(ctx, args)
	default:
		panic("unreachable")
	}
}

func (s *deltaChatService) chatsTool(ctx context.Context, args map[string]any) (any, error) {
	action, err := actionArgument(args, "list", "create", "update", "members", "mute", "invite", "join", "leave", "accept", "clear")
	if err != nil {
		return nil, err
	}
	switch action {
	case "list":
		return s.listConversations(ctx, args)
	case "create":
		return s.createChat(ctx, args)
	case "update":
		return s.updateChat(ctx, args)
	case "members":
		return s.listChatMembers(ctx, args)
	case "mute":
		return s.muteChat(ctx, args)
	case "invite":
		return s.getInviteLink(ctx, args)
	case "join":
		return s.joinInvite(ctx, args)
	case "leave":
		return s.leaveChat(ctx, args)
	case "accept":
		return s.acceptChat(ctx, args)
	case "clear":
		return s.clearChat(ctx, args)
	default:
		panic("unreachable")
	}
}

func (s *deltaChatService) messagesTool(ctx context.Context, args map[string]any) (any, error) {
	action, err := actionArgument(args, "receive", "read", "search", "send", "reply", "edit", "delete", "forward", "download", "react", "reactions", "receipts", "info")
	if err != nil {
		return nil, err
	}
	switch action {
	case "receive":
		return s.receivePendingMessages(ctx, args)
	case "read":
		return s.readConversation(ctx, args)
	case "search":
		return s.searchMessages(ctx, args)
	case "send":
		return s.sendMessage(ctx, args)
	case "reply":
		return s.replyMessage(ctx, args)
	case "edit":
		return s.editMessage(ctx, args)
	case "delete":
		return s.deleteMessages(ctx, args)
	case "forward":
		return s.forwardMessages(ctx, args)
	case "download":
		return s.downloadMessage(ctx, args)
	case "react":
		return s.reactMessage(ctx, args)
	case "reactions":
		return s.messageReactions(ctx, args)
	case "receipts":
		return s.messageReadReceipts(ctx, args)
	case "info":
		return s.messageInfo(ctx, args)
	default:
		panic("unreachable")
	}
}

func (s *deltaChatService) accountsTool(ctx context.Context, args map[string]any) (any, error) {
	action, err := actionArgument(args, "list", "create", "switch", "update", "remove")
	if err != nil {
		return nil, err
	}
	switch action {
	case "list":
		return s.listAccounts(ctx, args)
	case "create":
		return s.createAccount(ctx, args)
	case "switch":
		return s.switchAccount(ctx, args)
	case "update":
		return s.setAccountProfile(ctx, args)
	case "remove":
		return s.removeAccount(ctx, args)
	default:
		panic("unreachable")
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

func (s *deltaChatService) contactInfo(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	target, err := requiredString(args, "contact")
	if err != nil {
		return nil, err
	}
	contactID, err := s.client.LookupContact(ctx, accountID, target)
	if err != nil {
		return nil, err
	}
	contact, err := s.client.Contact(ctx, accountID, contactID)
	if err != nil {
		return nil, err
	}
	encryptionInfo, err := s.client.ContactEncryptionInfo(ctx, accountID, contactID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"account_id":      accountID,
		"contact":         contact,
		"encryption_info": encryptionInfo,
	}, nil
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

func (s *deltaChatService) listChatMembers(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	chatID, err := requiredUint32(args, "chat_id")
	if err != nil {
		return nil, err
	}
	chat, err := s.client.Chat(ctx, accountID, chatID)
	if err != nil {
		return nil, err
	}
	members, err := s.client.ChatMembers(ctx, accountID, chatID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"account_id":   accountID,
		"conversation": chat,
		"members":      members,
		"count":        len(members),
	}, nil
}

func (s *deltaChatService) muteChat(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	chatID, err := requiredUint32(args, "chat_id")
	if err != nil {
		return nil, err
	}
	muted, err := requiredBool(args, "muted")
	if err != nil {
		return nil, err
	}
	seconds, err := intArgument(args, "mute_seconds", 0, 31536000)
	if err != nil {
		return nil, err
	}
	var until time.Time
	if muted && seconds > 0 {
		until = time.Now().Add(time.Duration(seconds) * time.Second)
	}
	if err := s.client.SetChatMuted(ctx, accountID, chatID, muted, until); err != nil {
		return nil, err
	}
	muted, err = s.client.IsChatMuted(ctx, accountID, chatID)
	if err != nil {
		return nil, err
	}
	var mutedUntil any
	if !until.IsZero() {
		mutedUntil = until.Unix()
	}
	return map[string]any{
		"account_id":   accountID,
		"chat_id":      chatID,
		"muted":        muted,
		"muted_until":  mutedUntil,
		"mute_forever": muted && until.IsZero(),
	}, nil
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

func (s *deltaChatService) searchMessages(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	query, err := requiredString(args, "query")
	if err != nil {
		return nil, err
	}
	chatID, err := optionalUint32(args, "chat_id")
	if err != nil {
		return nil, err
	}
	limit, err := intArgument(args, "limit", 50, 500)
	if err != nil {
		return nil, err
	}
	messages, err := s.client.SearchMessages(ctx, accountID, query, chatID, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"account_id": accountID,
		"chat_id":    chatID,
		"query":      query,
		"messages":   messages,
		"count":      len(messages),
	}, nil
}

func (s *deltaChatService) editMessage(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	messageID, err := requiredUint32(args, "message_id")
	if err != nil {
		return nil, err
	}
	text, err := requiredString(args, "text")
	if err != nil {
		return nil, err
	}
	if err := s.client.StartIO(ctx, accountID); err != nil {
		return nil, err
	}
	if err := s.client.EditMessage(ctx, accountID, messageID, text); err != nil {
		return nil, err
	}
	return map[string]any{"account_id": accountID, "message_id": messageID, "edit_requested": true}, nil
}

func (s *deltaChatService) deleteMessages(ctx context.Context, args map[string]any) (any, error) {
	confirmed, err := boolArgument(args, "confirm", false)
	if err != nil || !confirmed {
		return nil, fmt.Errorf("confirm must be true to delete messages")
	}
	messageIDs, err := messageIDsArgument(args)
	if err != nil {
		return nil, err
	}
	forAll, err := boolArgument(args, "delete_for_all", false)
	if err != nil {
		return nil, err
	}
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	if err := s.client.StartIO(ctx, accountID); err != nil {
		return nil, err
	}
	if err := s.client.DeleteMessages(ctx, accountID, messageIDs, forAll); err != nil {
		return nil, err
	}
	return map[string]any{
		"account_id":      accountID,
		"message_ids":     messageIDs,
		"deleted":         true,
		"deleted_for_all": forAll,
	}, nil
}

func (s *deltaChatService) forwardMessages(ctx context.Context, args map[string]any) (any, error) {
	messageIDs, err := messageIDsArgument(args)
	if err != nil {
		return nil, err
	}
	recipient, err := requiredString(args, "recipient")
	if err != nil {
		return nil, err
	}
	accountID, err := s.configuredAccount(ctx, args)
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
	if err := s.client.ForwardMessages(ctx, accountID, messageIDs, chatID); err != nil {
		return nil, err
	}
	return map[string]any{
		"account_id":  accountID,
		"chat_id":     chatID,
		"message_ids": messageIDs,
		"forwarded":   true,
	}, nil
}

func (s *deltaChatService) downloadMessage(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	messageID, err := requiredUint32(args, "message_id")
	if err != nil {
		return nil, err
	}
	message, err := s.client.Message(ctx, accountID, messageID)
	if err != nil {
		return nil, err
	}
	requested := message.DownloadState == "Available" || message.DownloadState == "Failure" || message.DownloadState == ""
	if requested {
		if err := s.client.StartIO(ctx, accountID); err != nil {
			return nil, err
		}
		if err := s.client.DownloadMessage(ctx, accountID, messageID); err != nil {
			return nil, err
		}
		if current, err := s.client.Message(ctx, accountID, messageID); err == nil {
			message = current
		}
	}
	return map[string]any{
		"account_id":   accountID,
		"message":      message,
		"requested":    requested,
		"asynchronous": true,
	}, nil
}

func (s *deltaChatService) messageReadReceipts(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	messageID, err := requiredUint32(args, "message_id")
	if err != nil {
		return nil, err
	}
	receipts, count, err := s.client.MessageReadReceipts(ctx, accountID, messageID)
	if err != nil {
		return nil, err
	}
	contactIDs := make([]uint32, 0, len(receipts))
	seen := make(map[uint32]bool)
	for _, receipt := range receipts {
		if seen[receipt.ContactID] {
			continue
		}
		seen[receipt.ContactID] = true
		contactIDs = append(contactIDs, receipt.ContactID)
	}
	contacts, err := s.client.ContactsByIDs(ctx, accountID, contactIDs)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"account_id": accountID,
		"message_id": messageID,
		"receipts":   receipts,
		"contacts":   contacts,
		"count":      count,
	}, nil
}

func (s *deltaChatService) messageInfo(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	messageID, err := requiredUint32(args, "message_id")
	if err != nil {
		return nil, err
	}
	message, err := s.client.Message(ctx, accountID, messageID)
	if err != nil {
		return nil, err
	}
	info, err := s.client.MessageInformation(ctx, accountID, messageID)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"account_id": accountID, "message": message, "info": info}
	includeRaw, err := boolArgument(args, "include_raw", false)
	if err != nil {
		return nil, err
	}
	if includeRaw {
		raw, err := s.client.RawMessageInformation(ctx, accountID, messageID)
		if err != nil {
			return nil, err
		}
		result["raw_info"] = raw
	}
	return result, nil
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
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	recipient, err := requiredString(args, "recipient")
	if err != nil {
		return nil, err
	}
	text, filePath, filename, err := messageArguments(args)
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
	return s.client.Send(ctx, accountID, chatID, text, filePath, filename, 0)
}

func (s *deltaChatService) replyMessage(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	messageID, err := requiredUint32(args, "message_id")
	if err != nil {
		return nil, err
	}
	text, filePath, filename, err := messageArguments(args)
	if err != nil {
		return nil, err
	}
	if err := s.client.StartIO(ctx, accountID); err != nil {
		return nil, err
	}
	return s.client.Reply(ctx, accountID, messageID, text, filePath, filename)
}

func (s *deltaChatService) reactMessage(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	messageID, err := requiredUint32(args, "message_id")
	if err != nil {
		return nil, err
	}
	reactions, present, err := stringArrayArgument(args, "reactions")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, fmt.Errorf("reactions is required and must be an array of strings")
	}
	if err := s.client.StartIO(ctx, accountID); err != nil {
		return nil, err
	}
	reactionMessageID, err := s.client.SendReaction(ctx, accountID, messageID, reactions)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"account_id":          accountID,
		"message_id":          messageID,
		"reaction_message_id": reactionMessageID,
		"sent_reactions":      reactions,
	}, nil
}

func (s *deltaChatService) messageReactions(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	messageID, err := requiredUint32(args, "message_id")
	if err != nil {
		return nil, err
	}
	reactions, err := s.client.MessageReactions(ctx, accountID, messageID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"account_id": accountID, "message_id": messageID, "reactions": reactions}, nil
}

func (s *deltaChatService) createChat(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	chatType, err := requiredString(args, "chat_type")
	if err != nil {
		return nil, err
	}
	if chatType != "group" && chatType != "channel" {
		return nil, fmt.Errorf("chat_type must be group or channel")
	}
	name, err := requiredString(args, "name")
	if err != nil {
		return nil, err
	}
	members, _, err := s.memberIDs(ctx, accountID, args, "members")
	if err != nil {
		return nil, err
	}

	var chat deltachat.Chat
	switch chatType {
	case "group":
		chat, err = s.client.CreateGroup(ctx, accountID, name, members)
	case "channel":
		chat, err = s.client.CreateChannel(ctx, accountID, name, members)
	}
	if err != nil {
		return nil, err
	}
	if description, present, err := optionalStringArgument(args, "description"); err != nil {
		return nil, err
	} else if present {
		if err := s.client.SetChatDescription(ctx, accountID, chat.ID, description); err != nil {
			return nil, err
		}
	}
	if image, present, err := optionalStringArgument(args, "profile_image"); err != nil {
		return nil, err
	} else if present {
		if err := s.client.SetChatProfileImage(ctx, accountID, chat.ID, image); err != nil {
			return nil, err
		}
	}
	chat, err = s.client.Chat(ctx, accountID, chat.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"account_id": accountID, "conversation": chat, "created": true}, nil
}

func (s *deltaChatService) updateChat(ctx context.Context, args map[string]any) (any, error) {
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	chatID, err := requiredUint32(args, "chat_id")
	if err != nil {
		return nil, err
	}
	name, hasName, err := optionalStringArgument(args, "name")
	if err != nil {
		return nil, err
	}
	description, hasDescription, err := optionalStringArgument(args, "description")
	if err != nil {
		return nil, err
	}
	image, hasImage, err := optionalStringArgument(args, "profile_image")
	if err != nil {
		return nil, err
	}
	add, _, err := s.memberIDs(ctx, accountID, args, "add_members")
	if err != nil {
		return nil, err
	}
	remove, _, err := s.memberIDs(ctx, accountID, args, "remove_members")
	if err != nil {
		return nil, err
	}
	if !hasName && !hasDescription && !hasImage && len(add) == 0 && len(remove) == 0 {
		return nil, fmt.Errorf("update requires name, description, profile_image, add_members, or remove_members")
	}

	if hasName {
		if err := s.client.SetChatName(ctx, accountID, chatID, name); err != nil {
			return nil, err
		}
	}
	if hasDescription {
		if err := s.client.SetChatDescription(ctx, accountID, chatID, description); err != nil {
			return nil, err
		}
	}
	if hasImage {
		if err := s.client.SetChatProfileImage(ctx, accountID, chatID, image); err != nil {
			return nil, err
		}
	}
	for _, contactID := range add {
		if err := s.client.AddChatMember(ctx, accountID, chatID, contactID); err != nil {
			return nil, err
		}
	}
	for _, contactID := range remove {
		if err := s.client.RemoveChatMember(ctx, accountID, chatID, contactID); err != nil {
			return nil, err
		}
	}
	chat, err := s.client.Chat(ctx, accountID, chatID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"account_id": accountID, "conversation": chat, "updated": true}, nil
}

func (s *deltaChatService) leaveChat(ctx context.Context, args map[string]any) (any, error) {
	confirmed, err := boolArgument(args, "confirm", false)
	if err != nil || !confirmed {
		return nil, fmt.Errorf("confirm must be true to leave a group")
	}
	accountID, err := s.configuredAccount(ctx, args)
	if err != nil {
		return nil, err
	}
	chatID, err := requiredUint32(args, "chat_id")
	if err != nil {
		return nil, err
	}
	if err := s.client.LeaveGroup(ctx, accountID, chatID); err != nil {
		return nil, err
	}
	return map[string]any{"account_id": accountID, "chat_id": chatID, "left": true}, nil
}

func (s *deltaChatService) memberIDs(ctx context.Context, accountID uint32, args map[string]any, name string) ([]uint32, bool, error) {
	values, present, err := arrayArgument(args, name)
	if err != nil || !present {
		return nil, present, err
	}
	ids := make([]uint32, 0, len(values))
	seen := make(map[uint32]bool)
	for _, value := range values {
		var id uint32
		switch value := value.(type) {
		case string:
			id, err = s.client.ResolveContact(ctx, accountID, value)
		case float64:
			if value <= 0 || value > math.MaxUint32 || math.Trunc(value) != value {
				err = fmt.Errorf("%s entries must be positive contact IDs or contact strings", name)
			} else {
				id = uint32(value)
			}
		default:
			err = fmt.Errorf("%s entries must be positive contact IDs or contact strings", name)
		}
		if err != nil {
			return nil, true, err
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids, true, nil
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

func enumProperty(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": description}
}

func stringArrayProperty(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string"},
		"uniqueItems": true,
		"description": description,
	}
}

func uintArrayProperty(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "integer", "minimum": 1},
		"uniqueItems": true,
		"minItems":    1,
		"description": description,
	}
}

func membersProperty(description string) map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"oneOf": []any{
				map[string]any{"type": "integer", "minimum": 1},
				map[string]any{"type": "string", "minLength": 1},
			},
		},
		"description": description + " Each entry may be a contact ID, email address, or exact contact name.",
	}
}

func uintProperty(description string) map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "description": description}
}

func boundedIntegerProperty(description string, minimum, maximum int) map[string]any {
	return map[string]any{
		"type":        "integer",
		"minimum":     minimum,
		"maximum":     maximum,
		"description": description,
	}
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

func scopedMessageLimitProperty() map[string]any {
	return map[string]any{
		"type":        "integer",
		"minimum":     0,
		"maximum":     500,
		"description": "Maximum messages. For receive, omit or use 0 for all pending messages. For read and search, omit or use 0 for 50 messages.",
	}
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

func actionArgument(args map[string]any, allowed ...string) (string, error) {
	action, err := requiredString(args, "action")
	if err != nil {
		return "", err
	}
	for _, candidate := range allowed {
		if action == candidate {
			return action, nil
		}
	}
	return "", fmt.Errorf("action must be one of: %s", strings.Join(allowed, ", "))
}

func stringArgument(args map[string]any, name string) string {
	value, _ := args[name].(string)
	return strings.TrimSpace(value)
}

func optionalStringArgument(args map[string]any, name string) (string, bool, error) {
	raw, present := args[name]
	if !present || raw == nil {
		return "", false, nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", true, fmt.Errorf("%s must be a string", name)
	}
	return strings.TrimSpace(value), true, nil
}

func messageArguments(args map[string]any) (string, string, string, error) {
	text, _, err := optionalStringArgument(args, "text")
	if err != nil {
		return "", "", "", err
	}
	filePath, _, err := optionalStringArgument(args, "file_path")
	if err != nil {
		return "", "", "", err
	}
	filename, _, err := optionalStringArgument(args, "filename")
	if err != nil {
		return "", "", "", err
	}
	if text == "" && filePath == "" {
		return "", "", "", fmt.Errorf("text or file_path is required")
	}
	return text, filePath, filename, nil
}

func arrayArgument(args map[string]any, name string) ([]any, bool, error) {
	raw, present := args[name]
	if !present || raw == nil {
		return nil, false, nil
	}
	switch values := raw.(type) {
	case []any:
		return values, true, nil
	case []string:
		result := make([]any, len(values))
		for i := range values {
			result[i] = values[i]
		}
		return result, true, nil
	default:
		return nil, true, fmt.Errorf("%s must be an array", name)
	}
}

func stringArrayArgument(args map[string]any, name string) ([]string, bool, error) {
	values, present, err := arrayArgument(args, name)
	if err != nil || !present {
		return nil, present, err
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, raw := range values {
		value, ok := raw.(string)
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			return nil, true, fmt.Errorf("%s entries must be non-empty strings", name)
		}
		if !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	return result, true, nil
}

func messageIDsArgument(args map[string]any) ([]uint32, error) {
	ids := make([]uint32, 0)
	seen := make(map[uint32]bool)
	if id, err := optionalUint32(args, "message_id"); err != nil {
		return nil, err
	} else if id != 0 {
		ids = append(ids, id)
		seen[id] = true
	}
	values, present, err := arrayArgument(args, "message_ids")
	if err != nil {
		return nil, err
	}
	if present {
		for _, raw := range values {
			value, ok := raw.(float64)
			if !ok || value <= 0 || value > math.MaxUint32 || math.Trunc(value) != value {
				return nil, fmt.Errorf("message_ids entries must be positive integers")
			}
			id := uint32(value)
			if !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("message_id or message_ids is required")
	}
	return ids, nil
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

func requiredBool(args map[string]any, name string) (bool, error) {
	raw, ok := args[name]
	if !ok || raw == nil {
		return false, fmt.Errorf("%s is required and must be a boolean", name)
	}
	value, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return value, nil
}
