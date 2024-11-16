package pipe

import (
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type Session struct {
	Client     *Client
	Cmd        string
	Session    *ssh.Session
	StdinPipe  io.WriteCloser
	StdoutPipe io.Reader
	Done       chan struct{}
	In         chan []byte
	Out        chan []byte
	Timeout    time.Duration
	BufferSize int

	startOnce     sync.Once
	cleanDoneOnce sync.Once
	reconnectOnce sync.Once

	connectMu   sync.Mutex
	reconnectMu sync.Mutex
}

var _ io.ReadWriteCloser = (*Session)(nil)

func (s *Session) Open() error {
	s.Close()
	s.Client.Logger.Info("opening ssh session", "sessionCmd", s.Cmd)

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

	err = session.Start(s.Cmd)
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

func (s *Session) Close() error {
	s.Client.Logger.Info("closing session", "sessionCmd", s.Cmd)
	s.connectMu.Lock()
	defer s.connectMu.Unlock()

	var err error

	if s.Session != nil {
		err = s.Session.Close()
	}

	s.cleanDoneOnce.Do(func() {
		broadcastDone(s.Done, s.Timeout)

		for len(s.Done) > 0 {
			<-s.Done
		}
	})

	return err
}

func (s *Session) Reconnect() {
	s.reconnectMu.Lock()
	defer s.reconnectMu.Unlock()

	s.reconnectOnce.Do(func() {
		go func() {
			s.reconnectMu.Lock()
			defer s.reconnectMu.Unlock()

			for {
				err := s.Open()
				if err != nil {
					if s.Client != nil {
						err = s.Client.Open()
					}
				}

				if err == nil {
					break
				}

				time.Sleep(5 * time.Second)
			}

			s.reconnectOnce = sync.Once{}
		}()
	})
}

func (s *Session) Start() {
	s.startOnce.Do(func() {
		go func() {
			for {
				select {
				case <-s.Done:
					select {
					case s.Done <- struct{}{}:
						break
					case <-time.After(s.Timeout):
						break
					}
					return
				case data, ok := <-s.In:
					_, err := s.StdinPipe.Write(data)
					if !ok || err != nil {
						s.Client.Logger.Error("received error on write, reopening conn", "error", err)
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

					return
				default:
					data := make([]byte, 32*1024)

					n, err := s.StdoutPipe.Read(data)
					if err != nil {
						s.Client.Logger.Error("received error on read, reopening conn", "error", err)
						s.Reconnect()
						return
					}

					s.Out <- data[:n]
				}
			}
		}()
	})
}

func (s *Session) Write(data []byte) (int, error) {
	var (
		n   int
		err error
	)

	select {
	case s.In <- slices.Clone(data):
		n = len(data)
	case <-time.After(s.Timeout):
		err = fmt.Errorf("unable to send data within timeout")
	case <-s.Done:
		broadcastDone(s.Done, s.Timeout)
		break
	}

	return n, err
}

func (s *Session) Read(data []byte) (int, error) {
	var (
		n   int
		err error
	)

	select {
	case d := <-s.Out:
		n = copy(data, d)
	case <-time.After(s.Timeout):
		err = fmt.Errorf("unable to read data within timeout")
	case <-s.Done:
		broadcastDone(s.Done, s.Timeout)
		break
	}

	return n, err
}

func broadcastDone(done chan struct{}, timeout time.Duration) {
	select {
	case done <- struct{}{}:
		break
	case <-time.After(timeout):
		break
	}
}
