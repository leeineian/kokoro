package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// ============================================================================
// V2 Components
// ============================================================================

const (
	ComponentTypeSection      discord.ComponentType = 9
	ComponentTypeTextDisplay  discord.ComponentType = 10
	ComponentTypeThumbnail    discord.ComponentType = 11
	ComponentTypeMediaGallery discord.ComponentType = 12
	ComponentTypeFile         discord.ComponentType = 13
	ComponentTypeSeparator    discord.ComponentType = 14
	ComponentTypeContainer    discord.ComponentType = 17

	MessageFlagsIsComponentsV2 discord.MessageFlags = 1 << 15
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

func (m MediaGallery) Type() discord.ComponentType {
	return ComponentTypeMediaGallery
}

func (m MediaGallery) GetID() int {
	return 0
}

func (m MediaGallery) MarshalJSON() ([]byte, error) {
	type mediaGallery MediaGallery
	return json.Marshal(struct {
		mediaGallery
		Type discord.ComponentType `json:"type"`
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

func (t Thumbnail) Type() discord.ComponentType {
	return ComponentTypeThumbnail
}

func (t Thumbnail) GetID() int {
	return 0
}

func (t Thumbnail) MarshalJSON() ([]byte, error) {
	type thumbnail Thumbnail
	return json.Marshal(struct {
		thumbnail
		Type discord.ComponentType `json:"type"`
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

func (f File) Type() discord.ComponentType {
	return ComponentTypeFile
}

func (f File) GetID() int {
	return 0
}

func (f File) MarshalJSON() ([]byte, error) {
	type file File
	return json.Marshal(struct {
		file
		Type discord.ComponentType `json:"type"`
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

func (s Separator) Type() discord.ComponentType {
	return ComponentTypeSeparator
}

func (s Separator) GetID() int {
	return 0
}

func (s Separator) MarshalJSON() ([]byte, error) {
	type separator Separator
	return json.Marshal(struct {
		separator
		Type discord.ComponentType `json:"type"`
	}{
		separator: separator(s),
		Type:      s.Type(),
	})
}

// TextDisplay is a top-level component that allows you to add markdown-formatted text to the message.
type TextDisplay struct {
	Content string `json:"content"`
}

func (t TextDisplay) Type() discord.ComponentType {
	return ComponentTypeTextDisplay
}

func (t TextDisplay) GetID() int {
	return 0
}

func (t TextDisplay) MarshalJSON() ([]byte, error) {
	type textDisplay TextDisplay
	return json.Marshal(struct {
		textDisplay
		Type discord.ComponentType `json:"type"`
	}{
		textDisplay: textDisplay(t),
		Type:        t.Type(),
	})
}

// Section is a container-like component that groups other components with a header.
type Section struct {
	Components []interface{} `json:"components"`
	Accessory  interface{}   `json:"accessory,omitempty"`
}

func (s Section) Type() discord.ComponentType {
	return ComponentTypeSection
}

func (s Section) GetID() int {
	return 0
}

func (s Section) MarshalJSON() ([]byte, error) {
	type section Section
	return json.Marshal(struct {
		section
		Type discord.ComponentType `json:"type"`
	}{
		section: section(s),
		Type:    s.Type(),
	})
}

// Container is a top-level component that contains other components.
type Container struct {
	Components []interface{} `json:"components"`
}

func (c Container) Type() discord.ComponentType {
	return ComponentTypeContainer
}

func (c Container) GetID() int {
	return 0
}

func (c Container) MarshalJSON() ([]byte, error) {
	type container Container
	return json.Marshal(struct {
		container
		Type discord.ComponentType `json:"type"`
	}{
		container: container(c),
		Type:      c.Type(),
	})
}

// Helper functions for building components

func NewV2Container(components ...interface{}) Container {
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
func NewSection(content string, accessory interface{}) Section {
	s := Section{
		Components: []interface{}{NewTextDisplay(content)},
	}
	if accessory != nil {
		s.Accessory = accessory
	}
	return s
}

// EditInteractionV2 performs a manual PATCH request to edit the original interaction response,
func EditInteractionV2(client bot.Client, interaction discord.Interaction, container Container) error {
	route := rest.NewEndpoint(http.MethodPatch, "/webhooks/{application.id}/{interaction.token}/messages/@original")

	data := struct {
		Components []interface{}        `json:"components"`
		Flags      discord.MessageFlags `json:"flags"`
	}{
		Components: []interface{}{container},
		Flags:      MessageFlagsIsComponentsV2,
	}

	compiledRoute := route.Compile(nil, client.ApplicationID.String(), interaction.Token())

	return client.Rest.Do(compiledRoute, data, nil)
}

// RespondInteractionV2 responds to an interaction with ComponentsV2.
func RespondInteractionV2(client bot.Client, interaction discord.Interaction, container Container, ephemeral bool) error {
	route := rest.NewEndpoint(http.MethodPost, "/interactions/{interaction.id}/{interaction.token}/callback")

	var flags discord.MessageFlags = MessageFlagsIsComponentsV2
	if ephemeral {
		flags |= discord.MessageFlagEphemeral
	}

	data := struct {
		Type discord.InteractionResponseType `json:"type"`
		Data struct {
			Components []interface{}        `json:"components"`
			Flags      discord.MessageFlags `json:"flags"`
		} `json:"data"`
	}{
		Type: discord.InteractionResponseTypeCreateMessage,
		Data: struct {
			Components []interface{}        `json:"components"`
			Flags      discord.MessageFlags `json:"flags"`
		}{
			Components: []interface{}{container},
			Flags:      flags,
		},
	}

	compiledRoute := route.Compile(nil, interaction.ID().String(), interaction.Token())

	return client.Rest.Do(compiledRoute, data, nil)
}

// UpdateInteractionV2 updates the message component interaction with new ComponentsV2.
func UpdateInteractionV2(client bot.Client, interaction discord.Interaction, container Container) error {
	route := rest.NewEndpoint(http.MethodPost, "/interactions/{interaction.id}/{interaction.token}/callback")

	data := struct {
		Type discord.InteractionResponseType `json:"type"`
		Data struct {
			Components []interface{}        `json:"components"`
			Flags      discord.MessageFlags `json:"flags"`
		} `json:"data"`
	}{
		Type: discord.InteractionResponseTypeUpdateMessage,
		Data: struct {
			Components []interface{}        `json:"components"`
			Flags      discord.MessageFlags `json:"flags"`
		}{
			Components: []interface{}{container},
			Flags:      MessageFlagsIsComponentsV2,
		},
	}

	compiledRoute := route.Compile(nil, interaction.ID().String(), interaction.Token())

	return client.Rest.Do(compiledRoute, data, nil)
}

// SendMessageV2 sends a channel message using ComponentsV2.
func SendMessageV2(client bot.Client, channelID snowflake.ID, container Container, ref *discord.MessageReference) (*discord.Message, error) {
	route := rest.NewEndpoint(http.MethodPost, "/channels/{channel.id}/messages")

	data := struct {
		Components       []interface{}             `json:"components"`
		Flags            discord.MessageFlags      `json:"flags"`
		MessageReference *discord.MessageReference `json:"message_reference,omitempty"`
	}{
		Components:       []interface{}{container},
		Flags:            MessageFlagsIsComponentsV2,
		MessageReference: ref,
	}

	compiledRoute := route.Compile(nil, channelID.String())

	var msg discord.Message
	err := client.Rest.Do(compiledRoute, data, &msg)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

// EditMessageV2 edits an existing message to use ComponentsV2.
func EditMessageV2(client bot.Client, channelID, messageID snowflake.ID, container Container) (*discord.Message, error) {
	route := rest.NewEndpoint(http.MethodPatch, "/channels/{channel.id}/messages/{message.id}")

	data := struct {
		Components []interface{}        `json:"components"`
		Flags      discord.MessageFlags `json:"flags"`
	}{
		Components: []interface{}{container},
		Flags:      MessageFlagsIsComponentsV2,
	}

	compiledRoute := route.Compile(nil, channelID.String(), messageID.String())

	var msg discord.Message
	err := client.Rest.Do(compiledRoute, data, &msg)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

// ============================================================================
// Helper Functions
// ============================================================================

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func intPtr(i int) *int {
	return &i
}

func boolPtr(b bool) *bool {
	return &b
}

// ============================================================================
// Math & Logic
// ============================================================================

// Min returns the minimum of two integers.
func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Max returns the maximum of two integers.
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Atoi converts a string to an integer, returning 0 on error.
func Atoi(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}

// RandomIntRange returns a random integer in the range [min, max] inclusive.
func RandomIntRange(min, max int) int {
	if min > max {
		min, max = max, min
	}
	return rand.Intn(max-min+1) + min
}

// ============================================================================
// String Utilities
// ============================================================================

// Truncate truncates a string to the specified length with ellipsis at the end.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// TruncateCenter truncates a string keeping both the start and end.
func TruncateCenter(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(r[:maxLen])
	}
	k := (maxLen - 3) / 2
	return string(r[:k]) + "..." + string(r[len(r)-k:])
}

// TruncateWithPreserve truncates text while preserving a prefix and suffix.
func TruncateWithPreserve(text string, maxLen int, prefix, suffix string) string {
	rp, rs := []rune(prefix), []rune(suffix)
	fixedLen := len(rp) + len(rs)
	if fixedLen >= maxLen-10 {
		return TruncateCenter(prefix+text+suffix, maxLen)
	}
	return prefix + TruncateCenter(text, maxLen-fixedLen) + suffix
}

// ContainsIgnoreCase checks if a string contains a substring (case-insensitive).
func ContainsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(substr) == 0 ||
		(len(s) > 0 && ContainsLower(s, substr)))
}

// ContainsLower checks if a string contains a substring (case-insensitive).
// Both strings are converted to lowercase before comparison.
func ContainsLower(s, substr string) bool {
	s = strings.ToLower(s)
	substr = strings.ToLower(substr)
	return strings.Contains(s, substr)
}

// WrapText wraps text to fit within the specified width, breaking on word boundaries
func WrapText(text string, width int) []string {
	var lines []string
	words := strings.Fields(text)
	if len(words) == 0 {
		return lines
	}

	var sb strings.Builder
	currentLen := 0

	sb.WriteString(words[0])
	currentLen = len(words[0])

	for _, word := range words[1:] {
		wordLen := len(word)
		if currentLen+1+wordLen > width {
			lines = append(lines, sb.String())
			sb.Reset()
			sb.WriteString(word)
			currentLen = wordLen
		} else {
			sb.WriteString(" ")
			sb.WriteString(word)
			currentLen += 1 + wordLen
		}
	}
	lines = append(lines, sb.String())
	return lines
}

// ColorizeHex returns a colored hex string with a colored circle indicator.
func ColorizeHex(colorInt int) string {
	hex := fmt.Sprintf("#%06X", colorInt)
	r := (colorInt >> 16) & 0xFF
	g := (colorInt >> 8) & 0xFF
	b := colorInt & 0xFF
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm⬤ %s\x1b[0m", r, g, b, hex)
}

// ============================================================================
// Time Utilities
// ============================================================================

func FormatDuration(d time.Duration) string {
	if d == 0 {
		return "∞"
	}
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func ParseDuration(duration string) (time.Duration, error) {
	if duration == "" || duration == "0" {
		return 0, nil
	}
	re := regexp.MustCompile(`^(\d+)(s|m|h)?$`)
	m := re.FindStringSubmatch(strings.ToLower(duration))
	if m == nil {
		return 0, fmt.Errorf("invalid format")
	}
	v, _ := strconv.Atoi(m[1])
	switch m[2] {
	case "m":
		return time.Duration(v) * time.Minute, nil
	case "h":
		return time.Duration(v) * time.Hour, nil
	default:
		return time.Duration(v) * time.Second, nil
	}
}

func IntervalMsToDuration(ms int) time.Duration { return time.Duration(ms) * time.Millisecond }
