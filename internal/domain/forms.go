package domain

import "time"

// ChannelKind is where form submissions of a site are delivered.
type ChannelKind string

const (
	ChannelTelegram ChannelKind = "telegram"
	ChannelEmail    ChannelKind = "email"
	ChannelWebhook  ChannelKind = "webhook"
)

// Valid reports whether k is a known channel kind.
func (k ChannelKind) Valid() bool {
	switch k {
	case ChannelTelegram, ChannelEmail, ChannelWebhook:
		return true
	}
	return false
}

// ChannelStatus tells whether a channel has been confirmed by its owner.
type ChannelStatus string

const (
	ChannelPending ChannelStatus = "pending"
	ChannelActive  ChannelStatus = "active"
)

// FormChannel is one delivery target of a site. Target is a Telegram chat
// ID (empty while a link-based binding is pending), an email address or a
// webhook URL. SecretEnc is the sealed webhook signing secret.
type FormChannel struct {
	ID              int64
	SiteID          string
	Kind            ChannelKind
	Target          string
	SecretEnc       string
	Status          ChannelStatus
	Enabled         bool
	VerifyHash      []byte
	VerifyExpiresAt *time.Time
	VerifyAttempts  int
	// ConsecutiveFailures counts failed delivery attempts in a row;
	// PausedAt is set when the circuit breaker stopped deliveries to the
	// channel until its owner resumes it.
	ConsecutiveFailures int
	PausedAt            *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// FormField is one submitted field after merging repeated names.
type FormField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// FormSubmission is a stored form post.
type FormSubmission struct {
	ID         int64
	SiteID     string
	Host       string
	Form       string
	Fields     []FormField
	CreatedAt  time.Time
	Deliveries []FormDelivery
}

// DeliveryStatus is the state of one submission on one channel.
type DeliveryStatus string

const (
	DeliveryPending DeliveryStatus = "pending"
	DeliverySent    DeliveryStatus = "sent"
	DeliveryFailed  DeliveryStatus = "failed"
	// DeliveryCancelled marks a queued delivery dropped because its channel
	// was turned off or its site was deleted.
	DeliveryCancelled DeliveryStatus = "cancelled"
)

// FormDelivery is the queue entry of a submission for one channel.
type FormDelivery struct {
	ID            int64
	SubmissionID  int64
	ChannelID     int64
	ChannelKind   ChannelKind
	ChannelTarget string
	Status        DeliveryStatus
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	SentAt        *time.Time
}

// FormSiteSettings are per-site form options.
type FormSiteSettings struct {
	SiteID        string
	RequireAltcha bool
}
