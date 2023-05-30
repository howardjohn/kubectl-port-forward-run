package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
	"k8s.io/utils/exec"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/polymorphichelpers"
)

func init() {
}

var (
	configFlags = genericclioptions.NewConfigFlags(true)
)

var rootCmd = &cobra.Command{
	Use:          "kubectl port-forward-run TYPE/NAME REMOTE_PORT -- curl localhost:{}",
	Short:        "Plugin run a command with a port-forward established",
	Args:         cobra.MinimumNArgs(3),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		resourceRef := args[0]
		port := args[1]
		command := args[2]
		commandArgs := args[3:]
		builder := resource.NewBuilder(configFlags)
		namespace := *configFlags.Namespace
		if namespace == "" {
			dn, _, err := configFlags.ToRawKubeConfigLoader().Namespace()
			if err != nil {
				return cmdutil.UsageErrorf(cmd, err.Error())
			}
			namespace = dn
		}
		// Let the Builder do all the heavy-lifting.
		obj, err := builder.
			WithScheme(scheme.Scheme, scheme.Scheme.PrioritizedVersionsAllGroups()...).
			ContinueOnError().
			NamespaceParam(namespace).
			ResourceNames("pods", resourceRef).
			Do().
			Object()
		if err != nil {
			return cmdutil.UsageErrorf(cmd, err.Error())
		}
		getPodTimeout, err := cmdutil.GetPodRunningTimeoutFlag(cmd)
		if err != nil {
			return cmdutil.UsageErrorf(cmd, err.Error())
		}
		pod, err := polymorphichelpers.AttachablePodForObjectFn(configFlags, obj, getPodTimeout)
		if err != nil {
			return cmdutil.UsageErrorf(cmd, err.Error())
		}

		configFlags.ToRawKubeConfigLoader()
		rcfg, err := configFlags.ToRESTConfig()
		if err != nil {
			return cmdutil.UsageErrorf(cmd, err.Error())
		}
		rcfg = SetRestDefaults(rcfg)
		rc, err := rest.RESTClientFor(rcfg)
		if err != nil {
			return cmdutil.UsageErrorf(cmd, err.Error())
		}
		req := rc.Post().
			Resource("pods").
			Namespace(pod.Namespace).
			Name(pod.Name).
			SubResource("portforward")

		o := PortForwardOptions{
			Config:       rcfg,
			Address:      []string{"localhost"},
			Ports:        []string{":" + port},
			StopChannel:  make(chan struct{}),
			ReadyChannel: make(chan struct{}),
		}

		pfd := &defaultPortForwarder{genericclioptions.NewTestIOStreamsDiscard()}
		pf, err := pfd.ForwardPorts("POST", req.URL(), o)
		if err != nil {
			return cmdutil.UsageErrorf(cmd, err.Error())
		}

		replacePort(commandArgs, pf.LocalPort)
		c := exec.New().
			Command(command, commandArgs...)
		c.SetStdin(cmd.InOrStdin())
		c.SetStderr(cmd.ErrOrStderr())
		c.SetStdout(cmd.OutOrStdout())
		if err := c.Start(); err != nil {
			return cmdutil.UsageErrorf(cmd, err.Error())
		}
		cmdRes := make(chan error)
		go func() {
			cmdRes <- c.Wait()
		}()

		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt)
		defer signal.Stop(signals)

		select {
		case <-signals:
			c.Stop()
			close(o.StopChannel)
			return nil
		case e := <-cmdRes:
			return e
		}
	},
}

const (
	defaultPodAttachTimeout = 60 * time.Second
)

func replacePort(s []string, port uint16) {
	p := strconv.Itoa(int(port))
	for i, v := range s {
		s[i] = strings.ReplaceAll(v, "{}", p)
	}
}

func Execute() {
	flags := pflag.NewFlagSet("kubectl-resources", pflag.ExitOnError)
	pflag.CommandLine = flags
	cmdutil.AddPodRunningTimeoutFlag(rootCmd, defaultPodAttachTimeout)
	configFlags.AddFlags(flags)
	flags.AddFlagSet(rootCmd.PersistentFlags())
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func SetRestDefaults(config *rest.Config) *rest.Config {
	if config.GroupVersion == nil || config.GroupVersion.Empty() {
		config.GroupVersion = &corev1.SchemeGroupVersion
	}
	if len(config.APIPath) == 0 {
		if len(config.GroupVersion.Group) == 0 {
			config.APIPath = "/api"
		} else {
			config.APIPath = "/apis"
		}
	}
	if len(config.ContentType) == 0 {
		// Prefer to accept protobuf, but send JSON. This is due to some types (CRDs)
		// not accepting protobuf.
		// If we end up writing many core types in the future we may want to set ContentType to
		// ContentTypeProtobuf only for the core client.
		config.AcceptContentTypes = runtime.ContentTypeProtobuf + "," + runtime.ContentTypeJSON
		config.ContentType = runtime.ContentTypeJSON
	}
	if config.NegotiatedSerializer == nil {
		// This codec factory ensures the resources are not converted. Therefore, resources
		// will not be round-tripped through internal versions. Defaulting does not happen
		// on the client.
		config.NegotiatedSerializer = serializer.NewCodecFactory(scheme.Scheme).WithoutConversion()
	}

	return config
}
