package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"x-gateway/scripts/simple-ssh-fixture/server"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:0", "仅供验收的监听地址")
	flag.Parse()
	s, err := server.Start(*listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer s.Close()
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"address": s.Address(), "username": server.Username, "password": server.Password, "fingerprint": s.Fingerprint})
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	<-stop
}
