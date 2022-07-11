package utils

import (
	"os"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/klog/v2"
)

// KLogger is a logger that implements `pkg/log/Logger`, using klog
type KLogger struct {
}

// Log to stdout only if Debug is true.
func (logger KLogger) Debugf(format string, args ...interface{}) {
	klog.V(3).Infof(format+"\n", args...)
}

// Log to stdout only if Debug is true.
func (logger KLogger) Infof(format string, args ...interface{}) {
	klog.V(3).Infof(format+"\n", args...)
}

// Log to stdout always.
func (logger KLogger) Warnf(format string, args ...interface{}) {
	klog.Infof(format+"\n", args...)
}

// Log to stdout always.
func (logger KLogger) Errorf(format string, args ...interface{}) {
	klog.Infof(format+"\n", args...)
}

/////////////////////////////////////////////////////////////////////

func KLogIOStreams() genericclioptions.IOStreams {
	streams := genericclioptions.IOStreams{In: os.Stdin}
	streams.Out = WriteFunc(func(p []byte) (n int, err error) {
		klog.Infof("%s", p)
		return len(p), nil
	})
	streams.ErrOut = WriteFunc(func(p []byte) (n int, err error) {
		klog.Infof("ERROR: %s", p)
		return len(p), nil
	})

	return streams
}
