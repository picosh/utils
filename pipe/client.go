// Package pipe provides a simple way to create a bi-directional pipes
// over SSH. It uses the golang.org/x/crypto/ssh package to create a
// secure connection to the remote host and provides reconnect logic to all
// sessions in case of a connection drop.
package pipe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/antoniomika/syncmap"
	"golang.org/x/crypto/ssh"
)

// Client represents a connection to a remote host.
type Client struct {
	// Logger is the logger used by the client.
	Logger *slog.Logger

	// Info is the connection information.
	Info *SSHClientInfo

	// SSHClient is the underlying SSH client.
	SSHClient *ssh.Client

	// Sessions is a map of all sessions.
	Sessions *syncmap.Map[string, *Session]

	// connectMu is a mutex to protect the connection.
	connectMu sync.Mutex

	// ctx is the context of the client. If the context is canceled, the client will close
	// all sessions and the SSH connection. No reconnect will be attempted.
	Context context.Context

	// CtxDone is a channel that will send a time.Time when the main context is canceled.
	CtxDone chan time.Time
}

// NewClient creates a new pipe client.
func NewClient(ctx context.Context, logger *slog.Logger, info *SSHClientInfo) (*Client, error) {
	if ctx == nil {
		ctx = context.TODO()
	}

	c := &Client{
		Logger:  logger,
		Info:    info,
		Context: ctx,
		CtxDone: make(chan time.Time),
	}

	go func() {
		<-ctx.Done()
		close(c.CtxDone)
	}()

	err := c.Open()
	if err != nil {
		return nil, err
	}

	return c, nil
}

// Open opens the SSH connection.
func (c *Client) Open() error {
	c.Close()
	c.Logger.Info("opening ssh conn", "info", c.Info)

	c.connectMu.Lock()
	defer c.connectMu.Unlock()

	if c.Sessions == nil {
		c.Sessions = syncmap.New[string, *Session]()
	}

	sshClient, err := NewSSHClient(c.Info)
	if err != nil {
		return err
	}

	c.SSHClient = sshClient

	var errs []error
	c.Sessions.Range(func(key string, value *Session) bool {
		err := value.Open()
		if err != nil {
			c.Logger.Error("failed to open session", "id", key, "error", err)
		}
		errs = append(errs, err)
		return true
	})

	return errors.Join(errs...)
}

// Close closes the SSH connection.
func (c *Client) Close() error {
	c.Logger.Info("closing ssh conn", "info", c.Info)

	c.connectMu.Lock()
	defer c.connectMu.Unlock()

	var errs []error

	if c.Sessions != nil {
		c.Sessions.Range(func(key string, value *Session) bool {
			errs = append(errs, value.Close())
			return true
		})
	}

	if c.SSHClient != nil {
		errs = append(errs, c.SSHClient.Close())
	}

	return errors.Join(errs...)
}

// AddSession represents a bi-directional pipe over SSH.
// It creates a new session with the given id, command, buffer size and timeout.
// The buffer size is the size of the channel buffer for the input and output channels.
// Session implemnts the io.ReadWriteCloser interface and is resilient to network issues.
func (c *Client) AddSession(id string, command string, buffer int, readTimeout, writeTimeout time.Duration) (*Session, error) {
	if c.SSHClient == nil {
		return nil, fmt.Errorf("ssh client is not connected")
	}

	if buffer < 0 {
		buffer = 0
	}

	session := &Session{
		Logger:       c.Logger.With("id", id, "command", command),
		Client:       c,
		Command:      command,
		BufferSize:   buffer,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		Done:         make(chan struct{}),
		In:           make(chan SendData, buffer),
		Out:          make(chan SendData, buffer),
	}

	err := session.Open()
	if err != nil {
		return nil, err
	}

	s, _ := c.Sessions.LoadOrStore(id, session)

	return s, nil
}

// RemoveSession removes a session by id.
func (c *Client) RemoveSession(id string) error {
	if c.SSHClient == nil {
		return fmt.Errorf("ssh client is not connected")
	}

	var err error

	if session, ok := c.Sessions.Load(id); ok {
		err = session.Close()
		c.Sessions.Delete(id)
	}

	return err
}

// SSHClientInfo represents the SSH connection information.
type SSHClientInfo struct {
	RemoteHost     string
	RemoteHostname string
	RemoteUser     string
	KeyLocation    string
	KeyPassphrase  string
}

// NewSSHClient creates a new SSH client.
func NewSSHClient(info *SSHClientInfo) (*ssh.Client, error) {
	if info == nil {
		return nil, fmt.Errorf("conn info is invalid")
	}

	if !strings.Contains(info.RemoteHost, ":") {
		info.RemoteHost += ":22"
	}

	rawConn, err := net.Dial("tcp", info.RemoteHost)
	if err != nil {
		return nil, err
	}

	var signer ssh.Signer

	if info.KeyLocation != "" {
		keyPath, err := filepath.Abs(info.KeyLocation)
		if err != nil {
			return nil, err
		}

		data, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, err
		}

		if info.KeyPassphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(info.KeyPassphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(data)
		}

		if err != nil {
			return nil, err
		}
	}

	var authMethods []ssh.AuthMethod
	if signer != nil {
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(rawConn, info.RemoteHostname, &ssh.ClientConfig{
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		User:            info.RemoteUser,
	})

	if err != nil {
		return nil, err
	}

	sshClient := ssh.NewClient(sshConn, chans, reqs)

	return sshClient, nil
}

// Base is the base command for a simple bidirectional pipe.
func Base(ctx context.Context, logger *slog.Logger, info *SSHClientInfo, id, cmd string) (io.ReadWriteCloser, error) {
	client, err := NewClient(ctx, logger.With("info", info), info)
	if err != nil {
		return nil, err
	}

	session, err := client.AddSession(id, cmd, 0, -1, -1)
	if err != nil {
		return nil, err
	}

	go func() {
		<-ctx.Done()
		session.Close()
		client.Close()
	}()

	return session, nil
}

// Sub creates a new session with the given command..
func Sub(ctx context.Context, logger *slog.Logger, info *SSHClientInfo, cmd string) (io.Reader, error) {
	return Base(ctx, logger, info, "sub", cmd)
}

// Pub creates a new session with the given command.
func Pub(ctx context.Context, logger *slog.Logger, info *SSHClientInfo, cmd string) (io.WriteCloser, error) {
	return Base(ctx, logger, info, "pub", cmd)
}
