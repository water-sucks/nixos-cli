package system

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"strings"

	shlex "github.com/carapace-sh/carapace-shlex"
	"github.com/nix-community/nixos-cli/internal/logger"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type SSHSystem struct {
	client *ssh.Client
	sftp   *sftp.Client
	user   string
	host   string
	port   string
	logger logger.Logger
}

var ErrAgentNotStarted = fmt.Errorf("SSH_AUTH_SOCK not set; please start or forward an SSH agent")

func NewSSHSystem(host string, log logger.Logger) (*SSHSystem, error) {
	username := ""
	port := "22"
	address := host

	if host == "" {
		return nil, fmt.Errorf("host string is empty")
	}

	if at := strings.Index(address, "@"); at != -1 {
		username = address[:at]
		address = address[at+1:]
	}

	if colon := strings.Index(address, ":"); colon != -1 {
		port = address[colon+1:]
		address = address[:colon]
	}

	if username == "" {
		current, _ := user.Current()
		username = current.Username
	}

	sshAuthSock := os.Getenv("SSH_AUTH_SOCK")
	if sshAuthSock == "" {
		return nil, ErrAgentNotStarted
	}

	conn, err := net.Dial("unix", sshAuthSock)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SSH socket: %s", err)
	}
	agentClient := agent.NewClient(conn)

	auth := []ssh.AuthMethod{ssh.PublicKeysCallback(agentClient.Signers)}

	client, err := ssh.Dial("tcp", net.JoinHostPort(address, port), &ssh.ClientConfig{
		User: username,
		Auth: auth,
		// FIXME: use a better callback for known_hosts detection
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", host, err)
	}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to instantiate SFTP client: %w", err)
	}

	return &SSHSystem{
		client: client,
		sftp:   sftpClient,
		user:   username,
		host:   address,
		port:   port,
		logger: log,
	}, nil
}

func (s *SSHSystem) Logger() logger.Logger {
	return s.logger
}

func (s *SSHSystem) Run(cmd *Command) (int, error) {
	log := s.logger

	session, err := s.client.NewSession()
	if err != nil {
		return 0, fmt.Errorf("failed to create SSH session: %w", err)
	}

	defer func() {
		if err := session.Close(); err != nil && !errors.Is(err, io.EOF) {
			log.Debugf("failed to close SSH session cleanly: %v", err)
		}
	}()

	session.Stdin = cmd.Stdin
	session.Stdout = cmd.Stdout
	session.Stderr = cmd.Stderr

	for k, v := range cmd.Env {
		if err := session.Setenv(k, v); err != nil {
			s.logger.Debugf("warning: failed to set remote env %s: %v", k, err)
		}
	}

	argv := append([]string{cmd.Name}, cmd.Args...)
	fullCmd := shlex.Join(argv)

	err = session.Run(fullCmd)

	if err == nil {
		return 0, nil
	}

	if exitErr, ok := err.(*ssh.ExitError); ok {
		return exitErr.ExitStatus(), err
	}

	return 0, err
}

func (s *SSHSystem) IsNixOS() bool {
	_, err := s.sftp.Stat("/etc/NIXOS")
	if err == nil {
		return true
	}

	osReleaseFile, err := s.sftp.Open("/etc/os-release")
	if err != nil {
		return false
	}
	defer func() { _ = osReleaseFile.Close() }()

	osRelease, err := parseOSRelease(osReleaseFile)
	if err != nil {
		return false
	}

	distroID, ok := osRelease["ID"]
	if !ok {
		return false
	}

	return nixosDistroIDRegex.MatchString(distroID)
}

func (s *SSHSystem) Address() string {
	return fmt.Sprintf("%s@%s:%s", s.user, s.host, s.port)
}

func (s *SSHSystem) IsRemote() bool {
	return true
}

func (s *SSHSystem) Close() {
	_ = s.sftp.Close()
	_ = s.client.Close()
}
