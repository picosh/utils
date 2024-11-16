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

type Client struct {
	Logger    *slog.Logger
	Info      *SSHClientInfo
	SSHClient *ssh.Client
	Sessions  *syncmap.Map[string, *Session]

	Done          chan struct{}
	connectMu     sync.Mutex
	closeDoneOnce sync.Once
}

func NewClient(logger *slog.Logger, info *SSHClientInfo) (*Client, error) {
	c := &Client{
		Logger: logger,
		Info:   info,
	}

	err := c.Open()
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Client) Open() error {
	c.Close()
	c.Logger.Info("opening ssh conn", "info", c.Info)

	c.connectMu.Lock()
	defer c.connectMu.Unlock()

	c.closeDoneOnce = sync.Once{}
	c.Done = make(chan struct{})

	if c.Sessions == nil {
		c.Sessions = syncmap.New[string, *Session]()
	}

	sshClient, err := NewSSHClient(c.Info)
	if err != nil {
		return err
	}

	c.SSHClient = sshClient

	c.Sessions.Range(func(key string, value *Session) bool {
		value.Open()
		return true
	})

	return nil
}

func (c *Client) Close() error {
	c.Logger.Info("closing ssh conn", "info", c.Info)

	c.connectMu.Lock()
	defer c.connectMu.Unlock()

	if c.Done != nil {
		c.closeDoneOnce.Do(func() {
			close(c.Done)
			c.Done = nil
		})
	}

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

func (c *Client) AddSession(id string, cmd string, buffer int, timeout time.Duration) (*Session, error) {
	if c.SSHClient == nil {
		return nil, fmt.Errorf("ssh client is not connected")
	}

	if buffer < 0 {
		buffer = 0
	}

	if timeout < 0 {
		timeout = 10 * time.Millisecond
	}

	session := &Session{
		Client:     c,
		Cmd:        cmd,
		BufferSize: buffer,
		Timeout:    timeout,
		Done:       make(chan struct{}),
		In:         make(chan []byte, buffer),
		Out:        make(chan []byte, buffer),
	}

	err := session.Open()
	if err != nil {
		return nil, err
	}

	s, _ := c.Sessions.LoadOrStore(id, session)

	return s, nil
}

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

type SSHClientInfo struct {
	RemoteHost     string
	RemoteHostname string
	RemoteUser     string
	KeyLocation    string
	KeyPassphrase  string
}

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

func Base(id string, cmd string, ctx context.Context, info *SSHClientInfo) (io.ReadWriteCloser, error) {
	client, err := NewClient(slog.Default(), info)
	if err != nil {
		return nil, err
	}

	session, err := client.AddSession(id, cmd, 0, 0)
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

func Sub(cmd string, ctx context.Context, info *SSHClientInfo) (io.Reader, error) {
	return Base("sub", cmd, ctx, info)
}

func Pub(cmd string, ctx context.Context, info *SSHClientInfo) (io.WriteCloser, error) {
	return Base("pub", cmd, ctx, info)
}
