package utils

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	gocmd "github.com/go-cmd/cmd"
)

func PopenStream(ctx context.Context, command string) error {
	var theCmd *gocmd.Cmd
	cmdOptions := gocmd.Options{
		Buffered:  false,
		Streaming: true,
	}
	if runtime.GOOS == "windows" {
		theCmd = gocmd.NewCmdOptions(cmdOptions, "powershell", "-Command", command)
	} else {
		theCmd = gocmd.NewCmdOptions(cmdOptions, "sh", "-c", command)
	}

	doneChan := make(chan struct{})
	go func() {
		defer close(doneChan)
		for theCmd.Stdout != nil || theCmd.Stderr != nil {
			select {
			case line, open := <-theCmd.Stdout:
				if !open {
					theCmd.Stdout = nil
					continue
				}
				fmt.Fprintln(os.Stdout, line)
			case line, open := <-theCmd.Stderr:
				if !open {
					theCmd.Stderr = nil
					continue
				}
				fmt.Fprintln(os.Stderr, line)
			}
		}
	}()

	select {
	case <-ctx.Done():
		theCmd.Stop()
	case <-theCmd.Start():
	}
	<-doneChan
	fmt.Println()
	return nil
}

func ExpandPath(path string) (string, error) {
	// 处理空路径
	if path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}
	// 处理 ~ 符号（用户主目录）
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user home directory: %w", err)
		}
		// 处理不同形式的 ~ 符号
		if path == "~" {
			// 只有 ~ 符号
			return homeDir, nil
		} else if strings.HasPrefix(path, "~/") {
			// ~/path 形式
			path = filepath.Join(homeDir, path[2:])
		} else if strings.HasPrefix(path, "~\\") && runtime.GOOS == "windows" {
			// Windows 上的 ~\path 形式
			path = filepath.Join(homeDir, path[2:])
		} else {
			return "", fmt.Errorf("~username syntax is not fully supported, use ~/path instead")
		}
	}
	// 清理路径（移除多余的 . 和 ..，标准化分隔符）
	cleanPath := filepath.Clean(path)
	// 转换为绝对路径
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}
	// 在 Windows 上，确保路径使用正确的分隔符
	if runtime.GOOS == "windows" {
		absPath = filepath.Clean(absPath)
	}
	return absPath, nil
}
