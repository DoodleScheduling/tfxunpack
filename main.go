package main

import (
	"context"
	"errors"
	"log"
	"os"
	"runtime"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/sethvargo/go-envconfig"
	flag "github.com/spf13/pflag"
	"github.com/upbound/provider-terraform/apis/v1beta1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	"github.com/doodlescheduling/tfxunpack/internal/parser"
)

type Config struct {
	Log struct {
		Level    string `env:"LOG_LEVEL, default=info"`
		Encoding string `env:"LOG_ENCODING, default=json"`
	}
	File         string `env:"FILE, default=/dev/stdin"`
	Out          string `env:"OUTPUT"`
	FailFast     bool   `env:"FAIL_FAST"`
	AllowFailure bool   `env:"ALLOW_FAILURE"`
	Workers      int    `env:"WORKERS"`
}

var (
	config = &Config{}
)

func init() {
	flag.StringVarP(&config.Log.Level, "log-level", "l", "", "Define the log level (default is warning) [debug,info,warn,error]")
	flag.StringVarP(&config.Log.Encoding, "log-encoding", "e", "", "Define the log format (default is json) [json,console]")
	flag.StringVarP(&config.File, "file", "f", "", "Path to input")
	flag.StringVarP(&config.Out, "out", "o", "", "Path to output directory. If not set it will create a folder called tfmodule in the current directory.")
	flag.BoolVar(&config.AllowFailure, "allow-failure", false, "Do not exit > 0 if an error occurred")
	flag.BoolVar(&config.FailFast, "fail-fast", false, "Exit early if an error occurred")
	flag.IntVar(&config.Workers, "workers", 0, "Workers used to parse manifests")
}

func main() {
	ctx := context.TODO()
	if err := envconfig.Process(ctx, config); err != nil {
		log.Fatal(err)
	}

	flag.Parse()

	if config.Workers == 0 {
		config.Workers = runtime.NumCPU()
	}

	logger, err := buildLogger()
	must(err)

	f, err := os.Open(config.File)
	must(err)

	scheme := kruntime.NewScheme()
	v1beta1.SchemeBuilder.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	factory := serializer.NewCodecFactory(scheme)
	decoder := factory.UniversalDeserializer()

	out := config.Out
	if out == "" {
		out = "./tfmodule"
	}

	if _, err := os.Stat(out); err == nil {
		must(errors.New("output directory does already exists"))
	}

	err = os.MkdirAll(out, 0740)
	must(err)

	p := parser.Parser{
		Out:          out,
		AllowFailure: config.AllowFailure,
		FailFast:     config.FailFast,
		Workers:      config.Workers,
		Decoder:      decoder,
		Logger:       logger,
	}

	must(p.Run(context.TODO(), f))
}

func buildLogger() (logr.Logger, error) {
	logOpts := zap.NewDevelopmentConfig()
	logOpts.Encoding = config.Log.Encoding

	err := logOpts.Level.UnmarshalText([]byte(config.Log.Level))
	if err != nil {
		return logr.Discard(), err
	}

	zapLog, err := logOpts.Build()
	if err != nil {
		return logr.Discard(), err
	}

	return zapr.NewLogger(zapLog), nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
