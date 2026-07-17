package main

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

const (
	askpassAddressEnv = "ISHELL_ASKPASS_ADDRESS"
	askpassTokenEnv   = "ISHELL_ASKPASS_TOKEN"
)

type askpassServer struct {
	listener net.Listener
	token    string
	secrets  sessionSecrets
	once     sync.Once
}

type sessionSecrets struct {
	Password string `json:"password"`
	Script   string `json:"script"`
}

func startAskpassServer(secrets sessionSecrets) (*askpassServer, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	random := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, random); err != nil {
		listener.Close()
		return nil, err
	}
	server := &askpassServer{listener: listener, token: base64.RawURLEncoding.EncodeToString(random), secrets: secrets}
	go server.serve()
	return server, nil
}

func (s *askpassServer) serve() {
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(connection)
	}
}

func (s *askpassServer) handle(connection net.Conn) {
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(10 * time.Second))
	token, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(s.token)) != 1 {
		return
	}
	_ = json.NewEncoder(connection).Encode(s.secrets)
}

func (s *askpassServer) Close() {
	s.once.Do(func() { _ = s.listener.Close() })
}

// runAskpass is invoked only by OpenSSH through SSH_ASKPASS.
func runAskpass() {
	if len(os.Args) < 2 || !strings.Contains(strings.ToLower(os.Args[1]), "password") {
		os.Exit(1)
	}
	secrets, err := readSessionSecrets()
	password := secrets.Password
	if err != nil || password == "" {
		os.Exit(1)
	}
	fmt.Fprint(os.Stdout, password)
}

func readSessionSecret() (string, error) {
	secrets, err := readSessionSecrets()
	return secrets.Password, err
}

func readSessionSecrets() (sessionSecrets, error) {
	connection, err := net.DialTimeout("tcp", os.Getenv(askpassAddressEnv), 5*time.Second)
	if err != nil {
		return sessionSecrets{}, err
	}
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintln(connection, os.Getenv(askpassTokenEnv)); err != nil {
		return sessionSecrets{}, err
	}
	contents, err := io.ReadAll(io.LimitReader(connection, 1<<20))
	if err != nil {
		return sessionSecrets{}, err
	}
	var secrets sessionSecrets
	if err := json.Unmarshal(contents, &secrets); err != nil {
		return sessionSecrets{}, err
	}
	return secrets, nil
}

type telnetLoginState struct {
	user     string
	password string
	script   string
	stage    int
	tail     string
}

func (s *telnetLoginState) observe(value string) []string {
	s.tail = strings.ToLower(s.tail + value)
	if len(s.tail) > 512 {
		s.tail = s.tail[len(s.tail)-512:]
	}
	if s.stage == 0 && (strings.Contains(s.tail, "login:") || strings.Contains(s.tail, "username:")) && s.user != "" {
		s.stage = 1
		return []string{s.user + "\r\n"}
	}
	if s.stage < 2 && strings.Contains(s.tail, "password:") && s.password != "" {
		s.stage = 2
		responses := []string{s.password + "\r\n"}
		if s.script != "" {
			responses = append(responses, strings.ReplaceAll(s.script, "\n", "\r\n")+"\r\n")
		}
		return responses
	}
	return nil
}

func runTelnet(args []string) {
	if len(args) != 3 {
		fmt.Fprintln(os.Stderr, "ishell: telnet requires host, port, and user")
		os.Exit(1)
	}
	secrets := sessionSecrets{}
	if os.Getenv(askpassAddressEnv) != "" {
		var err error
		secrets, err = readSessionSecrets()
		if err != nil {
			fmt.Fprintln(os.Stderr, "ishell: read Telnet password:", err)
			os.Exit(1)
		}
	}
	connection, err := net.DialTimeout("tcp", net.JoinHostPort(args[0], args[1]), 10*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ishell: connect Telnet:", err)
		os.Exit(1)
	}
	defer connection.Close()
	if tcp, ok := connection.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(time.Minute)
	}

	if term.IsTerminal(int(os.Stdin.Fd())) {
		state, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err == nil {
			defer term.Restore(int(os.Stdin.Fd()), state)
		}
	}
	go func() { _, _ = io.Copy(connection, os.Stdin) }()

	login := telnetLoginState{user: args[2], password: secrets.Password, script: secrets.Script}
	readTelnet(connection, &login)
}

func readTelnet(connection net.Conn, login *telnetLoginState) {
	reader := bufio.NewReader(connection)
	for {
		value, err := reader.ReadByte()
		if err != nil {
			return
		}
		if value == 255 {
			if !handleTelnetCommand(reader, connection) {
				return
			}
			continue
		}
		_, _ = os.Stdout.Write([]byte{value})
		for _, response := range login.observe(string([]byte{value})) {
			_, _ = io.WriteString(connection, response)
		}
	}
}

func handleTelnetCommand(reader *bufio.Reader, connection net.Conn) bool {
	command, err := reader.ReadByte()
	if err != nil {
		return false
	}
	if command == 255 {
		_, _ = os.Stdout.Write([]byte{255})
		return true
	}
	if command == 250 {
		for {
			value, err := reader.ReadByte()
			if err != nil {
				return false
			}
			if value == 255 {
				next, err := reader.ReadByte()
				if err != nil || next == 240 {
					return err == nil
				}
			}
		}
	}
	if command != 251 && command != 252 && command != 253 && command != 254 {
		return true
	}
	option, err := reader.ReadByte()
	if err != nil {
		return false
	}
	response := byte(252) // WONT when the server sends DO/DONT.
	if command == 251 || command == 252 {
		response = 254 // DONT when the server sends WILL/WONT.
	}
	_, _ = connection.Write([]byte{255, response, option})
	return true
}
