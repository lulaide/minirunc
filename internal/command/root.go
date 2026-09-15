package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type application struct {
	debug     bool
	logFormat string
	logPath   string
	stdout    io.Writer
	stderr    io.Writer
	logger    *zap.Logger
	logFile   *os.File
}

type operationError struct {
	operation string
	err       error
}

func (e *operationError) Error() string { return e.err.Error() }
func (e *operationError) Unwrap() error { return e.err }

func operationFailed(operation string, err error) error {
	return &operationError{operation: operation, err: err}
}

// Execute runs the command line and returns a process exit code.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	app := &application{stdout: stdout, stderr: stderr, logFormat: "text"}
	root := app.rootCommand()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	executed, err := root.ExecuteContextC(ctx)
	if err == nil {
		app.closeLogger()
		return 0
	}

	var operation *operationError
	if errors.As(err, &operation) {
		if app.logger != nil {
			app.logger.Error("command failed",
				zap.String("command", commandPath(executed, root)),
				zap.String("operation", operation.operation),
				zap.Error(operation.err),
			)
		} else {
			fmt.Fprintf(stderr, "error: %v\n", operation.err)
		}
		app.closeLogger()
		return 1
	}

	fmt.Fprintf(stderr, "error: %v\n\n", err)
	if executed == nil {
		executed = root
	}
	fmt.Fprint(stderr, executed.UsageString())
	app.closeLogger()
	return 2
}

func (app *application) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "minirunc",
		Short: "A minimal OCI runtime for Linux containers",
		Long: "minirunc is a minimal Linux container runtime built for the OCI Runtime Specification.\n" +
			"It implements core runtime features including namespaces, rootfs, cgroups, process management, and security controls.",
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		SilenceErrors:     true,
		SilenceUsage:      true,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
		PersistentPreRunE: func(*cobra.Command, []string) error {
			if err := app.configureLogger(); err != nil {
				return err
			}
			return nil
		},
	}
	root.PersistentFlags().BoolVar(&app.debug, "debug", false, "enable debug logging")
	root.PersistentFlags().StringVar(&app.logPath, "log", "", "write logs to a file instead of stderr")
	root.PersistentFlags().StringVar(&app.logFormat, "log-format", "text", "log format: text or json")
	root.AddCommand(app.validateCommand())
	return root
}

func (app *application) configureLogger() error {
	var encoder zapcore.Encoder
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		MessageKey:     "message",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
	}
	switch app.logFormat {
	case "text":
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	case "json":
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	default:
		return fmt.Errorf("invalid log format %q: expected text or json", app.logFormat)
	}

	output := zapcore.AddSync(app.stderr)
	if app.logPath != "" {
		file, err := os.OpenFile(app.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return operationFailed("open log file", err)
		}
		app.logFile = file
		output = zapcore.AddSync(file)
	}
	level := zapcore.InfoLevel
	if app.debug {
		level = zapcore.DebugLevel
	}
	app.logger = zap.New(zapcore.NewCore(encoder, output, level))
	return nil
}

func (app *application) closeLogger() {
	if app.logger != nil {
		_ = app.logger.Sync()
	}
	if app.logFile != nil {
		_ = app.logFile.Close()
	}
}

func commandPath(executed, root *cobra.Command) string {
	if executed != nil {
		return executed.CommandPath()
	}
	return root.CommandPath()
}
