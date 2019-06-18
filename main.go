package main

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
	"gitlab.com/int128/kubectl-oidc-port-forward/portforward"
	"gitlab.com/int128/kubectl-oidc-port-forward/reverseproxy"
	"golang.org/x/sync/errgroup"
	"golang.org/x/xerrors"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	// https://github.com/kubernetes/client-go/issues/345
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

func startReverseProxyServer(ctx context.Context, eg *errgroup.Group, f *genericclioptions.ConfigFlags) error {
	config, err := f.ToRESTConfig()
	if err != nil {
		return xerrors.Errorf("could not load the config: %w", err)
	}
	token := config.AuthProvider.Config["id-token"]
	log.Printf("Using bearer token: %s", token)
	reverseproxy.Start(ctx, eg, 8888, reverseproxy.Target{
		Transport: &http.Transport{
			//TODO: set timeouts
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
		Scheme: "https",
		Port:   8443,
	}, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	return nil
}

func runPortForward(f *genericclioptions.ConfigFlags, osArgs []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)
	go func() {
		<-signals
		cancel()
	}()
	eg, ctx := errgroup.WithContext(ctx)
	if err := portforward.Start(ctx, eg, osArgs[1:]); err != nil {
		return xerrors.Errorf("could not start a kubectl process: %w", err)
	}
	if err := startReverseProxyServer(ctx, eg, f); err != nil {
		return xerrors.Errorf("could not start a reverse proxy server: %w", err)
	}
	if err := eg.Wait(); err != nil {
		return xerrors.Errorf("error while port-forwarding: %w", err)
	}
	return nil
}

func run(osArgs []string) int {
	var exitCode int
	f := genericclioptions.NewConfigFlags()
	rootCmd := cobra.Command{
		Use:     "kubectl oidc-port-forward TYPE/NAME [options] LOCAL_PORT:REMOTE_PORT",
		Short:   "Forward one or more local ports to a pod",
		Example: `  kubectl -n kube-system oidc-port-forward svc/kubernetes-dashboard 8443:443`,
		Args:    cobra.MinimumNArgs(2),
		Run: func(*cobra.Command, []string) {
			if err := runPortForward(f, osArgs); err != nil {
				log.Printf("error: %s", err)
				exitCode = 1
			}
		},
	}
	f.AddFlags(rootCmd.PersistentFlags())

	rootCmd.Version = "v0.0.1"
	rootCmd.SetArgs(osArgs[1:])
	if err := rootCmd.Execute(); err != nil {
		return 1
	}
	return exitCode
}

func main() {
	os.Exit(run(os.Args))
}
