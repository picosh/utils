package pipe

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Session represents a session to a remote command.
type Session struct {
	Context context.Context
	Logger  *slog.Logger
	Client  *Client

	Done chan struct{}
	In   chan SendData
	Out  chan SendData

	ReadTimeout  time.Duration
	WriteTimeout time.Duration

	ID         string
	Command    string
	BufferSize int

	Session    *ssh.Session
	StdinPipe  io.WriteCloser
	StdoutPipe io.Reader

	startOnce     sync.Once
	cleanDoneOnce sync.Once
	reconnectOnce sync.Once

	connectMu   sync.Mutex
	reconnectMu sync.Mutex

	cancelFunc context.CancelFunc
}

type SendData struct {
	Data  []byte
	Error error
	N     int
}

var _ io.ReadWriteCloser = (*Session)(nil)

// Open opens a new session.
func (s *Session) Open() error {
	s.Close()
	s.Logger.Info("opening ssh session")

	s.connectMu.Lock()
	defer s.connectMu.Unlock()

	if s.Client == nil {
		return fmt.Errorf("client is nil")
	}

	if s.Client.SSHClient == nil {
		return fmt.Errorf("ssh client is nil")
	}

	session, err := s.Client.SSHClient.NewSession()
	if err != nil {
		return err
	}

	stdinPipe, err := session.StdinPipe()
	if err != nil {
		return err
	}

	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		return err
	}

	err = session.Start(s.Command)
	if err != nil {
		return err
	}

	s.Session = session
	s.StdinPipe = stdinPipe
	s.StdoutPipe = stdoutPipe

	s.startOnce = sync.Once{}
	s.cleanDoneOnce = sync.Once{}

	s.Start()

	return nil
}

// Close closes the session.
func (s *Session) Close() error {
	s.Logger.Info("closing session")
	s.connectMu.Lock()
	defer s.connectMu.Unlock()

	var err error

	if s.Session != nil {
		err = s.Session.Close()
	}

	s.cleanDoneOnce.Do(func() {
		s.broadcastDone()

		for len(s.Done) > 0 {
			<-s.Done
		}
	})

	return err
}

// Reconnect reconnects the session.
func (s *Session) Reconnect() {
	s.reconnectMu.Lock()
	defer s.reconnectMu.Unlock()

	s.reconnectOnce.Do(func() {
		go func() {
			s.reconnectMu.Lock()
			defer func() {
				s.reconnectOnce = sync.Once{}
				s.reconnectMu.Unlock()
			}()

		loop:
			for {
				select {
				case <-s.Client.CtxDone:
					return
				case <-s.Context.Done():
					return
				default:
					err := s.Open()
					if err != nil {
						if s.Client != nil {
							err = s.Client.Open()
						}
					}

					if err == nil {
						break loop
					}

					time.Sleep(5 * time.Second)
				}
			}
		}()
	})
}

// Start starts the session handling.
func (s *Session) Start() {
	s.startOnce.Do(func() {
		go func() {
			for {
				select {
				case <-s.Done:
					s.broadcastDone()
					return
				case <-s.Client.CtxDone:
					s.broadcastDone()
					return
				case <-s.Context.Done():
					s.broadcastDone()
					return
				case data, ok := <-s.In:
					_, err := s.StdinPipe.Write(data.Data)
					if !ok || err != nil || data.Error != nil {
						s.Logger.Error("received error on write, reopening conn", "error", err)
						s.Reconnect()
						return
					}
				}
			}
		}()

		go func() {
			for {
				select {
				case <-s.Done:
					s.broadcastDone()
					return
				case <-s.Client.CtxDone:
					s.broadcastDone()
					return
				case <-s.Context.Done():
					s.broadcastDone()
					return
				default:
					data := make([]byte, 32*1024)

					n, err := s.StdoutPipe.Read(data)

					select {
					case s.Out <- SendData{Data: data[:n], N: n, Error: err}:
						break
					case <-s.Done:
						s.broadcastDone()
						return
					case <-s.Client.CtxDone:
						s.broadcastDone()
						return
					case <-s.Context.Done():
						s.broadcastDone()
						return
					}

					if err != nil {
						s.Logger.Error("received error on read, reopening conn", "error", err)
						s.Reconnect()
						return
					}
				}
			}
		}()
	})
}

// Write writes data to the session.
func (s *Session) Write(data []byte) (int, error) {
	var (
		n   int
		err error
	)

	select {
	case s.In <- SendData{Data: slices.Clone(data), N: len(data)}:
		n = len(data)
	case <-s.Done:
		s.broadcastDone()
		break
	case <-s.Client.CtxDone:
		s.broadcastDone()
		break
	case <-s.Context.Done():
		s.broadcastDone()
		break
	case <-s.writeTimeout():
		break
	}

	return n, err
}

// Read reads data from the session.
func (s *Session) Read(data []byte) (int, error) {
	var (
		n   int
		err error
	)

	select {
	case d := <-s.Out:
		n = copy(data, d.Data)
		err = d.Error
	case <-s.Done:
		s.broadcastDone()
		break
	case <-s.Client.CtxDone:
		s.broadcastDone()
		break
	case <-s.Context.Done():
		s.broadcastDone()
		break
	case <-s.readTimeout():
		break
	}

	return n, err
}

// Cancel cancels the session.
func (s *Session) Cancel() {
	s.cancelFunc()
}

func (s *Session) readTimeout() <-chan time.Time {
	if s.ReadTimeout < 0 {
		return s.Client.CtxDone
	}

	return time.After(s.ReadTimeout)
}

func (s *Session) writeTimeout() <-chan time.Time {
	if s.WriteTimeout < 0 {
		return s.Client.CtxDone
	}

	return time.After(s.WriteTimeout)
}

func (s *Session) broadcastDone() {
	select {
	case s.Done <- struct{}{}:
		break
	case <-s.readTimeout():
		break
	case <-s.writeTimeout():
		break
	case <-s.Client.CtxDone:
		break
	case <-s.Context.Done():
		break
	default:
		break
	}
}
