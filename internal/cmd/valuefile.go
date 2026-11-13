package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

type valueFormat struct {
	noun     string
	maxBytes int64
	trim     bool
}

var (
	textValue = valueFormat{noun: "value", maxBytes: 256 << 10, trim: true}
	jsonValue = valueFormat{noun: "JSON", maxBytes: 4 << 20}
)

type valueFile struct {
	flag   string
	format valueFormat
}

func textFile(flag string) valueFile { return valueFile{flag: flag, format: textValue} }

func jsonFile(flag string) valueFile { return valueFile{flag: flag, format: jsonValue} }

func (v valueFile) name() string { return v.flag + "-file" }

func withValueFiles(cmd *cobra.Command, files ...valueFile) *cobra.Command {
	for _, file := range files {
		cmd.Flags().String(file.name(), "", fmt.Sprintf("read the --%s %s from `path`, or - for stdin (max %s)", file.flag, file.format.noun, byteSize(file.format.maxBytes)))
		cmd.MarkFlagsMutuallyExclusive(file.flag, file.name())
		_ = cmd.MarkFlagFilename(file.name())
	}
	run := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := loadValueFiles(cmd, files); err != nil {
			return err
		}
		return run(cmd, args)
	}
	return cmd
}

func loadValueFiles(cmd *cobra.Command, files []valueFile) error {
	var given []valueFile
	stdin := ""
	for _, file := range files {
		if !cmd.Flags().Changed(file.name()) {
			continue
		}
		path, _ := cmd.Flags().GetString(file.name())
		switch {
		case path == "":
			return fmt.Errorf("--%s needs a path, or - for stdin", file.name())
		case path == "-" && stdin != "":
			return fmt.Errorf("--%s and --%s both read stdin; pass one of them a file path", stdin, file.name())
		case path == "-":
			stdin = file.name()
		}
		given = append(given, file)
	}
	for _, file := range given {
		path, _ := cmd.Flags().GetString(file.name())
		value, err := readValueFile(cmd.InOrStdin(), path, file.format)
		if err != nil {
			return fmt.Errorf("--%s: %w", file.name(), err)
		}
		if err := cmd.Flags().Set(file.flag, value); err != nil {
			return err
		}
	}
	return nil
}

func readValueFile(stdin io.Reader, path string, format valueFormat) (string, error) {
	source, r := "stdin", stdin
	if path != "-" {
		source = path
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("cannot read %s: %w", path, pathCause(err))
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s is not a regular file", path)
		}
		if info.Size() > format.maxBytes {
			return "", fmt.Errorf("%s exceeds %s", path, byteSize(format.maxBytes))
		}
		file, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("cannot read %s: %w", path, pathCause(err))
		}
		defer file.Close()
		r = file
	}
	data, err := io.ReadAll(io.LimitReader(r, format.maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", source, pathCause(err))
	}
	if int64(len(data)) > format.maxBytes {
		return "", fmt.Errorf("%s exceeds %s", source, byteSize(format.maxBytes))
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		return "", fmt.Errorf("%s is not valid UTF-8", source)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("%s contains a NUL byte", source)
	}
	value := string(data)
	if format.trim {
		value = strings.TrimRight(value, "\r\n")
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is empty", source)
	}
	return value, nil
}

func pathCause(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}

func byteSize(n int64) string {
	if n%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d KiB", n>>10)
}
