package service

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"text/template"
	"time"
)

type Service struct {
	URLTemplate string
	CmdTemplate []string
	Timeout     time.Duration
	url         string
	process     *os.Process
}

type addressInfo struct {
	Address string
	Host    string
	Port    string
}

func (s *Service) URL() (string, error) {
	if s.process == nil {
		return "", errors.New("not running")
	}

	return s.url, nil
}

func (s *Service) Start() error {
	if s.process != nil {
		return errors.New("already running")
	}

	address, err := freeAddress()
	if err != nil {
		return fmt.Errorf("failed to locate a free port: %s", err)
	}

	if s.url, err = buildURL(s.URLTemplate, address); err != nil {
		return fmt.Errorf("failed to parse URL: %s", err)
	}

	command, err := buildCommand(s.CmdTemplate, address)
	if err != nil {
		return fmt.Errorf("failed to parse command: %s", err)
	}

	if err := command.Start(); err != nil {
		return fmt.Errorf("failed to run command: %s", err)
	}

	s.process = command.Process

	return s.waitForServer()
}

func (s *Service) Stop() {
	if s.process == nil {
		return
	}
	s.process.Signal(syscall.SIGINT)
	s.process.Wait()
	s.process = nil
}

func buildURL(url string, address addressInfo) (string, error) {
	urlTemplate, err := template.New("URL").Parse(url)
	if err != nil {
		return "", err
	}
	urlBuffer := &bytes.Buffer{}
	if err := urlTemplate.Execute(urlBuffer, address); err != nil {
		return "", err
	}
	return urlBuffer.String(), nil
}

func buildCommand(arguments []string, address addressInfo) (*exec.Cmd, error) {
	if len(arguments) == 0 {
		return nil, errors.New("empty command")
	}

	command := []string{}
	for _, argument := range arguments {
		argTemplate, err := template.New("command").Parse(argument)
		if err != nil {
			return nil, err
		}

		argBuffer := &bytes.Buffer{}
		if err := argTemplate.Execute(argBuffer, address); err != nil {
			return nil, err
		}
		command = append(command, argBuffer.String())
	}

	return exec.Command(command[0], command[1:]...), nil
}

func freeAddress() (addressInfo, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return addressInfo{}, err
	}
	defer listener.Close()

	address := listener.Addr().String()
	addressParts := strings.SplitN(address, ":", 2)
	return addressInfo{address, addressParts[0], addressParts[1]}, nil
}

func (s *Service) waitForServer() error {
	timeoutChan := time.After(s.Timeout)
	failedChan := make(chan struct{}, 1)
	startedChan := make(chan struct{})

	go func() {
		up := s.checkStatus()
		for !up {
			select {
			case <-failedChan:
				return
			default:
				time.Sleep(500 * time.Millisecond)
				up = s.checkStatus()
			}
		}
		startedChan <- struct{}{}
	}()

	select {
	case <-timeoutChan:
		failedChan <- struct{}{}
		s.Stop()
		return errors.New("failed to start before timeout")
	case <-startedChan:
		return nil
	}
}

func (s *Service) checkStatus() bool {
	client := &http.Client{}
	request, _ := http.NewRequest("GET", fmt.Sprintf("%s/status", s.url), nil)
	response, err := client.Do(request)
	if err == nil && response.StatusCode == 200 {
		return true
	}
	return false
}
