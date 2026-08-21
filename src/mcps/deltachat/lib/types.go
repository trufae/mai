package deltachat

// Account is a Delta Chat profile in the account store.
type Account struct {
	Kind         string `json:"kind"`
	ID           uint32 `json:"id"`
	DisplayName  string `json:"displayName,omitempty"`
	Address      string `json:"addr,omitempty"`
	ProfileImage string `json:"profileImage,omitempty"`
	Color        string `json:"color,omitempty"`
	PrivateTag   string `json:"privateTag,omitempty"`
}

// Contact is a known Delta Chat contact.
type Contact struct {
	ID              uint32  `json:"id"`
	Address         string  `json:"address"`
	AuthName        string  `json:"authName"`
	Color           string  `json:"color"`
	Name            string  `json:"name"`
	DisplayName     string  `json:"displayName"`
	NameAndAddress  string  `json:"nameAndAddr"`
	ProfileImage    string  `json:"profileImage,omitempty"`
	Status          string  `json:"status,omitempty"`
	IsBlocked       bool    `json:"isBlocked"`
	IsKeyContact    bool    `json:"isKeyContact"`
	EncryptionReady bool    `json:"e2eeAvail"`
	IsVerified      bool    `json:"isVerified"`
	IsBot           bool    `json:"isBot"`
	LastSeen        int64   `json:"lastSeen,omitempty"`
	VerifierID      *uint32 `json:"verifierId,omitempty"`
	WasSeenRecently bool    `json:"wasSeenRecently"`
}

// Chat describes a Delta Chat conversation.
type Chat struct {
	ID                  uint32   `json:"id"`
	Name                string   `json:"name"`
	ChatType            string   `json:"chatType"`
	ContactIDs          []uint32 `json:"contactIds,omitempty"`
	IsEncrypted         bool     `json:"isEncrypted"`
	IsContactRequest    bool     `json:"isContactRequest"`
	IsDeviceChat        bool     `json:"isDeviceChat"`
	IsMuted             bool     `json:"isMuted"`
	IsSelfTalk          bool     `json:"isSelfTalk"`
	Archived            bool     `json:"archived"`
	Pinned              bool     `json:"pinned"`
	CanSend             bool     `json:"canSend"`
	FreshMessageCounter int      `json:"freshMessageCounter"`
	ProfileImage        string   `json:"profileImage,omitempty"`
}

// Message is the useful subset of a Delta Chat message returned by the RPC
// server. File contains the local attachment path when present.
type Message struct {
	ID                    uint32        `json:"id"`
	ChatID                uint32        `json:"chatId"`
	FromID                uint32        `json:"fromId"`
	Text                  string        `json:"text"`
	Subject               string        `json:"subject,omitempty"`
	Timestamp             int64         `json:"timestamp"`
	SortTimestamp         int64         `json:"sortTimestamp"`
	ReceivedTimestamp     int64         `json:"receivedTimestamp,omitempty"`
	ViewType              string        `json:"viewType,omitempty"`
	State                 uint32        `json:"state,omitempty"`
	SystemMessageType     string        `json:"systemMessageType,omitempty"`
	DownloadState         string        `json:"downloadState,omitempty"`
	Duration              int32         `json:"duration,omitempty"`
	DimensionsHeight      int32         `json:"dimensionsHeight,omitempty"`
	DimensionsWidth       int32         `json:"dimensionsWidth,omitempty"`
	IsInfo                bool          `json:"isInfo"`
	IsBot                 bool          `json:"isBot"`
	IsEdited              bool          `json:"isEdited"`
	IsForwarded           bool          `json:"isForwarded"`
	HasDeviatingTimestamp bool          `json:"hasDeviatingTimestamp"`
	HasHTML               bool          `json:"hasHtml"`
	HasLocation           bool          `json:"hasLocation"`
	ShowPadlock           bool          `json:"showPadlock"`
	InfoContactID         *uint32       `json:"infoContactId,omitempty"`
	OriginalMessageID     *uint32       `json:"originalMsgId,omitempty"`
	ParentID              *uint32       `json:"parentId,omitempty"`
	SavedMessageID        *uint32       `json:"savedMessageId,omitempty"`
	OverrideSenderName    string        `json:"overrideSenderName,omitempty"`
	Sender                Contact       `json:"sender"`
	Quote                 *MessageQuote `json:"quote,omitempty"`
	Reactions             *Reactions    `json:"reactions,omitempty"`
	VCardContact          *VCardContact `json:"vcardContact,omitempty"`
	WebxdcHref            string        `json:"webxdcHref,omitempty"`
	File                  string        `json:"file,omitempty"`
	FileName              string        `json:"fileName,omitempty"`
	FileMIME              string        `json:"fileMime,omitempty"`
	FileBytes             uint64        `json:"fileBytes,omitempty"`
	Error                 string        `json:"error,omitempty"`
	LoadError             string        `json:"loadError,omitempty"`
}

// MessageQuote describes the message or text quoted by a reply.
type MessageQuote struct {
	Kind               string `json:"kind"`
	Text               string `json:"text"`
	MessageID          uint32 `json:"messageId,omitempty"`
	ChatID             uint32 `json:"chatId,omitempty"`
	AuthorDisplayName  string `json:"authorDisplayName,omitempty"`
	AuthorDisplayColor string `json:"authorDisplayColor,omitempty"`
	OverrideSenderName string `json:"overrideSenderName,omitempty"`
	ViewType           string `json:"viewType,omitempty"`
	Image              string `json:"image,omitempty"`
	IsForwarded        bool   `json:"isForwarded,omitempty"`
}

// VCardContact is the contact preview embedded in a vCard message.
type VCardContact struct {
	Address      string `json:"addr"`
	DisplayName  string `json:"displayName"`
	Color        string `json:"color"`
	Key          string `json:"key,omitempty"`
	ProfileImage string `json:"profileImage,omitempty"`
	Timestamp    *int64 `json:"timestamp,omitempty"`
}

// EphemeralTimer describes the disappearing-message timer associated with a
// message.
type EphemeralTimer struct {
	Kind     string `json:"kind"`
	Duration uint32 `json:"duration,omitempty"`
}

// MessageInfo contains transport and expiry information for a message.
type MessageInfo struct {
	EphemeralTimer     EphemeralTimer `json:"ephemeralTimer"`
	EphemeralTimestamp *int64         `json:"ephemeralTimestamp,omitempty"`
	Error              string         `json:"error,omitempty"`
	HopInfo            string         `json:"hopInfo"`
	RFC724MessageID    string         `json:"rfc724Mid"`
	ServerURLs         []string       `json:"serverUrls"`
}

// MessageReadReceipt records when a contact read a message.
type MessageReadReceipt struct {
	ContactID uint32 `json:"contactId"`
	Timestamp int64  `json:"timestamp"`
}

// LoginParams configures a conventional email account. Address and Password
// are sufficient for providers that support autoconfiguration.
type LoginParams struct {
	Address           string `json:"addr"`
	Password          string `json:"password"`
	IMAPServer        string `json:"imapServer,omitempty"`
	IMAPPort          uint16 `json:"imapPort,omitempty"`
	IMAPFolder        string `json:"imapFolder,omitempty"`
	IMAPSecurity      string `json:"imapSecurity,omitempty"`
	IMAPUser          string `json:"imapUser,omitempty"`
	SMTPServer        string `json:"smtpServer,omitempty"`
	SMTPPort          uint16 `json:"smtpPort,omitempty"`
	SMTPSecurity      string `json:"smtpSecurity,omitempty"`
	SMTPUser          string `json:"smtpUser,omitempty"`
	SMTPPassword      string `json:"smtpPassword,omitempty"`
	CertificateChecks string `json:"certificateChecks,omitempty"`
	OAuth2            bool   `json:"oauth2,omitempty"`
}

// CreateAccountOptions describes either a new chatmail account or a
// conventional email login. With zero values a chatmail account is created on
// DefaultChatmailRelay.
type CreateAccountOptions struct {
	Relay        string
	Login        *LoginParams
	DisplayName  string
	ProfileImage string
}

// SentMessage identifies a newly queued outgoing message.
type SentMessage struct {
	AccountID uint32  `json:"account_id"`
	ChatID    uint32  `json:"chat_id"`
	MessageID uint32  `json:"message_id"`
	Message   Message `json:"message"`
}

// Reaction summarizes one emoji reaction on a message.
type Reaction struct {
	Emoji      string `json:"emoji"`
	Count      uint   `json:"count"`
	IsFromSelf bool   `json:"isFromSelf"`
}

// Reactions contains aggregate reactions and the reactions by contact ID.
type Reactions struct {
	Reactions          []Reaction          `json:"reactions"`
	ReactionsByContact map[string][]string `json:"reactionsByContact"`
}
