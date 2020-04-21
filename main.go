package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/sirupsen/logrus"
)

func main() {
	var stopCh = make(chan error, 1)
	var sigCh = make(chan os.Signal, 1)

	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	plugin := newNoopDevicePlugin()
	go func() {
		stopCh <- plugin.start()
	}()

	// TODO(fuweid): add retry?
	if err := plugin.Register(); err != nil {
		panic(err)
	}

	logrus.Info("registered into kubelet")

	select {
	case err := <-stopCh:
		logrus.Error(err)
	case sig := <-sigCh:
		logrus.Warnf("received %v signal, and closed it", sig)
	}
}
