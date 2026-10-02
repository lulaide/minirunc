package container

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// 父子进程不共享 Go 对象，父进程通过管道发送这份配置。
// RootfsPath 是宿主侧的绝对路径；Spec 保留 OCI 配置供初始化进程使用。
// clone flags 属于父进程启动子进程时的参数，不需要通过配置管道传递。
type initConfig struct {
	RootfsPath string      `json:"rootfsPath"`
	Spec       *specs.Spec `json:"spec"`
}

// Ready 表示当前初始化步骤成功，不等同于 OCI running 状态。
// 成功时 Ready=true 且 Error 为空；失败时 Ready=false 且 Error 非空。
// 独立传输结果，避免把空消息或子进程退出误认为成功。
type initResult struct {
	Ready bool   `json:"ready"`
	Error string `json:"error,omitempty"`
}

// 写完消息后，调用方必须关闭管道写端。这里不关闭，因为管道的创建和
// 生命周期属于调用方；writeInitMessage 只负责 JSON 编码和写入。
func writeInitMessage(writer io.Writer, message any) error {
	if err := json.NewEncoder(writer).Encode(message); err != nil {
		return fmt.Errorf("write init message: %w", err)
	}
	return nil
}

func readInitConfig(reader io.Reader) (*initConfig, error) {
	var config *initConfig
	if err := readInitMessage(reader, &config); err != nil {
		return nil, err
	}
	if err := validateInitConfig(config); err != nil {
		return nil, err
	}
	return config, nil
}

func validateInitConfig(config *initConfig) error {
	if config == nil || config.Spec == nil {
		return errors.New("init config: OCI spec is required")
	}
	if !filepath.IsAbs(config.RootfsPath) || strings.ContainsRune(config.RootfsPath, '\x00') {
		return errors.New("init config: rootfsPath must be an absolute path without NUL")
	}
	return nil
}

func readInitResult(reader io.Reader) (*initResult, error) {
	var result *initResult
	if err := readInitMessage(reader, &result); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("init result: expected a JSON object")
	}
	if result.Ready && result.Error != "" {
		return nil, errors.New("init result: ready result must not contain an error")
	}
	if !result.Ready && result.Error == "" {
		return nil, errors.New("init result: failed result must contain an error")
	}
	return result, nil
}

func readInitMessage(reader io.Reader, message any) error {
	// 每条管道只传一份消息。读取到 EOF 表示发送方已关闭写端，整份消息
	// 已经传完；因此截断的 JSON、空管道和连续两份 JSON 都会解析失败。
	data, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("read init message: %w", err)
	}
	if err := json.Unmarshal(data, message); err != nil {
		return fmt.Errorf("decode init message: %w", err)
	}
	return nil
}
