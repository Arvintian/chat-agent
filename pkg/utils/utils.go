package utils

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func PopenStream(command string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-Command", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}
	}()

	return cmd.Wait()
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
