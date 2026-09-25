package godave

import "log/slog"

type Callbacks interface {
	SendMLSKeyPackage(mlsKeyPackage []byte) error
	SendMLSCommitWelcome(mlsCommitWelcome []byte) error
	SendReadyForTransition(transitionID uint16) error
	SendInvalidCommitWelcome(transitionID uint16) error
}

type ChannelID uint64

type Codec int

const (
	CodecOpus Codec = 1
)

type Session interface {
	MaxSupportedProtocolVersion() int
	Ready() bool
	Close() error
	SetChannelID(channelID ChannelID)
	AssignSsrcToCodec(ssrc uint32, codec Codec)
	MaxEncryptedFrameSize(frameSize int) int
	Encrypt(ssrc uint32, frame []byte, encryptedFrame []byte) (int, error)
	MaxDecryptedFrameSize(userID UserID, frameSize int) int
	Decrypt(userID UserID, frame []byte, decryptedFrame []byte) (int, error)
	AddUser(userID UserID)
	RemoveUser(userID UserID)
	OnSelectProtocolAck(protocolVersion uint16)
	OnDavePrepareTransition(transitionID uint16, protocolVersion uint16)
	OnDaveExecuteTransition(protocolVersion uint16)
	OnDavePrepareEpoch(epoch int, protocolVersion uint16)
	OnDaveMLSExternalSenderPackage(externalSenderPackage []byte)
	OnDaveMLSProposals(proposals []byte)
	OnDaveMLSPrepareCommitTransition(transitionID uint16, commitMessage []byte)
	OnDaveMLSWelcome(transitionID uint16, welcomeMessage []byte)
}

type SessionCreateFunc func(logger *slog.Logger, userID UserID, callbacks Callbacks) Session

type UserID string

func NewNoopSession(logger *slog.Logger, _ UserID, _ Callbacks) Session {
	return nil
}
