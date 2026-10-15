package config

import (
	"flag"
)

// Flags holds the command-line flags shared by the subcommands.
type Flags struct {
	Config        string
	Listen        string
	AllowRemote   bool
	MaxConcurrent int
	Binary        string
	LogFormat     string
	LogLevel      string
	set           map[string]bool
}

// RegisterFlags defines the serve flags on fs.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	fs.StringVar(&f.Config, "config", "", "path of the JSON configuration file")
	fs.StringVar(&f.Listen, "listen", "", "listen address (default "+DefaultListen+")")
	fs.BoolVar(&f.AllowRemote, "allow-remote", false, "allow listening on a non-loopback address")
	fs.IntVar(&f.MaxConcurrent, "max-concurrent", 0, "maximum number of concurrent CLI processes")
	fs.StringVar(&f.Binary, "claude-bin", "", "path or name of the CLI binary")
	fs.StringVar(&f.LogFormat, "log-format", "", "log format: text|json")
	fs.StringVar(&f.LogLevel, "log-level", "", "log level: debug|info|warn|error")
	return f
}

// MarkSet records which flags were explicitly given; call after fs.Parse.
func (f *Flags) MarkSet(fs *flag.FlagSet) {
	f.set = map[string]bool{}
	fs.Visit(func(fl *flag.Flag) { f.set[fl.Name] = true })
}

func (f *Flags) isSet(name string) bool { return f != nil && f.set[name] }
