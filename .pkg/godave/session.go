package godave

import (
	"log/slog"
	"strconv"
	"sync"

	dave "github.com/FlameInTheDark/go-dave"
)

type pureSession struct {
	logger    *slog.Logger
	userID    string
	callbacks Callbacks
	daveSess  *dave.DAVESession

	mu sync.Mutex
}

func NewSession(logger *slog.Logger, userID UserID, callbacks Callbacks) Session {
	s := &pureSession{
		logger:    logger,
		userID:    string(userID),
		callbacks: callbacks,
	}
	// DAVE protocol version 1 is standard
	ds, err := dave.NewDAVESession(1, string(userID), "", nil)
	if err != nil {
		if logger != nil {
			logger.Error("failed to create pure dave session", "err", err)
		}
	}
	s.daveSess = ds
	return s
}

func (s *pureSession) MaxSupportedProtocolVersion() int { return 1 }
func (s *pureSession) Ready() bool                      { return s.daveSess != nil && s.daveSess.Ready() }
func (s *pureSession) Close() error                     { return nil }

func (s *pureSession) SetChannelID(channelID ChannelID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Create a new session with the updated channel ID
	ds, _ := dave.NewDAVESession(1, s.userID, strconv.FormatUint(uint64(channelID), 10), nil)
	s.daveSess = ds
}

func (s *pureSession) AssignSsrcToCodec(ssrc uint32, codec Codec) {}

func (s *pureSession) MaxEncryptedFrameSize(frameSize int) int {
	return frameSize + 128 // Approximate max overhead for DAVE
}

func (s *pureSession) Encrypt(ssrc uint32, frame []byte, encryptedFrame []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.daveSess.Ready() {
		copy(encryptedFrame, frame)
		return len(frame), nil
	}
	encrypted, err := s.daveSess.EncryptOpus(frame)
	if err != nil {
		return 0, err
	}
	n := copy(encryptedFrame, encrypted)
	return n, nil
}

func (s *pureSession) MaxDecryptedFrameSize(userID UserID, frameSize int) int {
	return frameSize
}

func (s *pureSession) Decrypt(userID UserID, frame []byte, decryptedFrame []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.daveSess.Ready() {
		copy(decryptedFrame, frame)
		return len(frame), nil
	}
	decrypted, err := s.daveSess.Decrypt(string(userID), dave.MediaTypeAudio, frame)
	if err != nil {
		return 0, err
	}
	n := copy(decryptedFrame, decrypted)
	return n, nil
}

func (s *pureSession) AddUser(userID UserID)    {}
func (s *pureSession) RemoveUser(userID UserID) {}

func (s *pureSession) OnSelectProtocolAck(protocolVersion uint16) {}

func (s *pureSession) OnDavePrepareTransition(transitionID uint16, protocolVersion uint16) {
	s.callbacks.SendReadyForTransition(transitionID)
}

func (s *pureSession) OnDaveExecuteTransition(protocolVersion uint16) {}

func (s *pureSession) OnDavePrepareEpoch(epoch int, protocolVersion uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pkg, err := s.daveSess.GetSerializedKeyPackage()
	if err == nil && pkg != nil {
		s.callbacks.SendMLSKeyPackage(pkg)
	}
}

func (s *pureSession) OnDaveMLSExternalSenderPackage(externalSenderPackage []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.daveSess.SetExternalSender(externalSenderPackage)
}

func (s *pureSession) OnDaveMLSProposals(proposals []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Pass all users except ourselves, or let the library handle it
	users := []string{s.userID}
	cw, err := s.daveSess.ProcessProposals(dave.ProposalsAppend, proposals, users)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("failed to process proposals", "err", err)
		}
		return
	}
	if cw != nil {
		packet, err := dave.EncodeCommitWelcomePacket(cw.Commit, cw.Welcome)
		if err == nil {
			s.callbacks.SendMLSCommitWelcome(packet)
		}
	}
}

func (s *pureSession) OnDaveMLSPrepareCommitTransition(transitionID uint16, commitMessage []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.daveSess.ProcessCommit(commitMessage); err != nil {
		s.callbacks.SendInvalidCommitWelcome(transitionID)
		return
	}
	s.callbacks.SendReadyForTransition(transitionID)
}

func (s *pureSession) OnDaveMLSWelcome(transitionID uint16, welcomeMessage []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.daveSess.ProcessWelcome(welcomeMessage); err != nil {
		s.callbacks.SendInvalidCommitWelcome(transitionID)
		return
	}
	s.callbacks.SendReadyForTransition(transitionID)
}
