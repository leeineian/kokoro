package sys

import (
	"encoding/json"

	"github.com/bwmarrin/discordgo"
)

// Constants for V2 Components from discordgo commit 5b2d241
const (
	ComponentTypeSection      discordgo.ComponentType = 9
	ComponentTypeTextDisplay  discordgo.ComponentType = 10
	ComponentTypeThumbnail    discordgo.ComponentType = 11
	ComponentTypeMediaGallery discordgo.ComponentType = 12
	ComponentTypeFile         discordgo.ComponentType = 13
	ComponentTypeSeparator    discordgo.ComponentType = 14
	ComponentTypeContainer    discordgo.ComponentType = 17

	// MessageFlagsIsComponentsV2 indicates the message uses the new components system.
	MessageFlagsIsComponentsV2 discordgo.MessageFlags = 1 << 15
)

// UnfurledMediaItem represents an unfurled media item.
type UnfurledMediaItem struct {
	URL string `json:"url"`
}

// MediaGalleryItem represents an item in a media gallery.
type MediaGalleryItem struct {
	Media       UnfurledMediaItem `json:"media"`
	Description *string           `json:"description,omitempty"`
	Spoiler     bool              `json:"spoiler,omitempty"`
}

// MediaGallery is a top-level component that allows you to group images, videos or gifs into a gallery grid.
type MediaGallery struct {
	ID    int                `json:"id,omitempty"`
	Items []MediaGalleryItem `json:"items"`
}

func (m MediaGallery) Type() discordgo.ComponentType {
	return ComponentTypeMediaGallery
}

func (m MediaGallery) MarshalJSON() ([]byte, error) {
	type mediaGallery MediaGallery
	return json.Marshal(struct {
		mediaGallery
		Type discordgo.ComponentType `json:"type"`
	}{
		mediaGallery: mediaGallery(m),
		Type:         m.Type(),
	})
}

// Thumbnail is a component that displays a single image/thumbnail.
type Thumbnail struct {
	Media       UnfurledMediaItem `json:"media"`
	Description *string           `json:"description,omitempty"`
	Spoiler     bool              `json:"spoiler,omitempty"`
}

func (t Thumbnail) Type() discordgo.ComponentType {
	return ComponentTypeThumbnail
}

func (t Thumbnail) MarshalJSON() ([]byte, error) {
	type thumbnail Thumbnail
	return json.Marshal(struct {
		thumbnail
		Type discordgo.ComponentType `json:"type"`
	}{
		thumbnail: thumbnail(t),
		Type:      t.Type(),
	})
}

// File is a component that represents a file attachment.
type File struct {
	File        UnfurledMediaItem `json:"file"`
	Description *string           `json:"description,omitempty"`
	Spoiler     bool              `json:"spoiler,omitempty"`
	Filename    string            `json:"filename,omitempty"`
}

func (f File) Type() discordgo.ComponentType {
	return ComponentTypeFile
}

func (f File) MarshalJSON() ([]byte, error) {
	type file File
	return json.Marshal(struct {
		file
		Type discordgo.ComponentType `json:"type"`
	}{
		file: file(f),
		Type: f.Type(),
	})
}

// Separator is a component that renders a visual separator or spacing.
type Separator struct {
	Divider bool             `json:"divider,omitempty"`
	Spacing SeparatorSpacing `json:"spacing,omitempty"`
}

func (s Separator) Type() discordgo.ComponentType {
	return ComponentTypeSeparator
}

func (s Separator) MarshalJSON() ([]byte, error) {
	type separator Separator
	return json.Marshal(struct {
		separator
		Type discordgo.ComponentType `json:"type"`
	}{
		separator: separator(s),
		Type:      s.Type(),
	})
}

// TextDisplay is a top-level component that allows you to add markdown-formatted text to the message.
type TextDisplay struct {
	Content string `json:"content"`
}

func (t TextDisplay) Type() discordgo.ComponentType {
	return ComponentTypeTextDisplay
}

func (t TextDisplay) MarshalJSON() ([]byte, error) {
	type textDisplay TextDisplay
	return json.Marshal(struct {
		textDisplay
		Type discordgo.ComponentType `json:"type"`
	}{
		textDisplay: textDisplay(t),
		Type:        t.Type(),
	})
}

// Section is a container-like component that groups other components with a header.
// It consists of Components (usually TextDisplay) and an Accessory (optional component like a button).
type Section struct {
	Components []discordgo.MessageComponent `json:"components"`
	Accessory  discordgo.MessageComponent   `json:"accessory,omitempty"`
}

func (s Section) Type() discordgo.ComponentType {
	return ComponentTypeSection
}

func (s Section) MarshalJSON() ([]byte, error) {
	type section Section
	return json.Marshal(struct {
		section
		Type discordgo.ComponentType `json:"type"`
	}{
		section: section(s),
		Type:    s.Type(),
	})
}

// Container is a top-level component that contains other components.
type Container struct {
	Components []discordgo.MessageComponent `json:"components"`
}

func (c Container) Type() discordgo.ComponentType {
	return ComponentTypeContainer
}

func (c Container) MarshalJSON() ([]byte, error) {
	type container Container
	// We need to marshal the components manually to ensure they are marshaled with their types if needed,
	// but discordgo.MessageComponent marshaling usually handles it if the structs implement MarshalJSON.
	return json.Marshal(struct {
		container
		Type discordgo.ComponentType `json:"type"`
	}{
		container: container(c),
		Type:      c.Type(),
	})
}

// Helper functions for building components

func NewV2Container(components ...discordgo.MessageComponent) Container {
	return Container{
		Components: components,
	}
}

func NewTextDisplay(content string) TextDisplay {
	return TextDisplay{
		Content: content,
	}
}

func NewMediaGallery(urls ...string) MediaGallery {
	items := make([]MediaGalleryItem, len(urls))
	for i, url := range urls {
		items[i] = MediaGalleryItem{
			Media: UnfurledMediaItem{
				URL: url,
			},
		}
	}
	return MediaGallery{
		Items: items,
	}
}

func NewThumbnail(url string) Thumbnail {
	return Thumbnail{
		Media: UnfurledMediaItem{
			URL: url,
		},
	}
}

func NewFile(url string, filename string) File {
	return File{
		File: UnfurledMediaItem{
			URL: url,
		},
		Filename: filename,
	}
}

// SeparatorSpacing defines the spacing size for a separator.
type SeparatorSpacing int

const (
	SeparatorSpacingSmall  SeparatorSpacing = 0
	SeparatorSpacingMedium SeparatorSpacing = 1 // default
	SeparatorSpacingLarge  SeparatorSpacing = 2
)

func NewSeparator(divider bool) Separator {
	return Separator{
		Divider: divider,
	}
}

func NewSeparatorWithSpacing(divider bool, spacing SeparatorSpacing) Separator {
	return Separator{
		Divider: divider,
		Spacing: spacing,
	}
}

// NewSection creates a new Section component.
// It automatically wraps the content string into a TextDisplay component.
func NewSection(content string, accessory discordgo.MessageComponent) Section {
	return Section{
		Components: []discordgo.MessageComponent{NewTextDisplay(content)},
		Accessory:  accessory,
	}
}

// EditInteractionV2 performs a manual PATCH request to edit the original interaction response,
// ensuring that the MessageFlagsIsComponentsV2 flag is included in the payload.
func EditInteractionV2(s *discordgo.Session, i *discordgo.Interaction, container Container) error {
	endpoint := discordgo.EndpointWebhookMessage(i.AppID, i.Token, "@original")

	// Custom struct to include Flags in the edit payload
	data := struct {
		Components []discordgo.MessageComponent `json:"components"`
		Flags      discordgo.MessageFlags       `json:"flags"`
	}{
		Components: []discordgo.MessageComponent{container},
		Flags:      MessageFlagsIsComponentsV2,
	}

	body, err := json.Marshal(data)
	if err != nil {
		return err
	}

	_, err = s.RequestWithLockedBucket("PATCH", endpoint, "application/json", body, s.Ratelimiter.LockBucket(endpoint), 0)
	return err
}

// RespondInteractionV2 responds to an interaction with ComponentsV2.
// It wraps the container in the correct structure and sends it.
func RespondInteractionV2(s *discordgo.Session, i *discordgo.Interaction, container Container, ephemeral bool) error {
	var flags discordgo.MessageFlags = MessageFlagsIsComponentsV2
	if ephemeral {
		flags |= discordgo.MessageFlagsEphemeral
	}

	data := &discordgo.InteractionResponseData{
		Components: []discordgo.MessageComponent{container},
		Flags:      flags,
	}

	return s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: data,
	})
}

// UpdateInteractionV2 updates the message component interaction with new ComponentsV2.
func UpdateInteractionV2(s *discordgo.Session, i *discordgo.Interaction, container Container) error {
	return s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Components: []discordgo.MessageComponent{container},
			Flags:      MessageFlagsIsComponentsV2,
		},
	})
}

// SendMessageV2 sends a channel message using ComponentsV2.
func SendMessageV2(s *discordgo.Session, channelID string, container Container, ref *discordgo.MessageReference) (*discordgo.Message, error) {
	endpoint := discordgo.EndpointChannelMessages(channelID)

	data := struct {
		Components       []discordgo.MessageComponent `json:"components"`
		Flags            discordgo.MessageFlags       `json:"flags"`
		MessageReference *discordgo.MessageReference  `json:"message_reference,omitempty"`
	}{
		Components:       []discordgo.MessageComponent{container},
		Flags:            MessageFlagsIsComponentsV2,
		MessageReference: ref,
	}

	body, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	response, err := s.RequestWithLockedBucket("POST", endpoint, "application/json", body, s.Ratelimiter.LockBucket(endpoint), 0)
	if err != nil {
		return nil, err
	}

	var msg discordgo.Message
	if err := json.Unmarshal(response, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// EditMessageV2 edits an existing message to use ComponentsV2.
func EditMessageV2(s *discordgo.Session, channelID, messageID string, container Container) (*discordgo.Message, error) {
	endpoint := discordgo.EndpointChannelMessage(channelID, messageID)

	data := struct {
		Components []discordgo.MessageComponent `json:"components"`
		Flags      discordgo.MessageFlags       `json:"flags"`
	}{
		Components: []discordgo.MessageComponent{container},
		Flags:      MessageFlagsIsComponentsV2,
	}

	body, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	response, err := s.RequestWithLockedBucket("PATCH", endpoint, "application/json", body, s.Ratelimiter.LockBucket(endpoint), 0)
	if err != nil {
		return nil, err
	}

	var msg discordgo.Message
	if err := json.Unmarshal(response, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}
