package gc

import (
	"context"
	"fmt"
	"github.com/jenkins-x-plugins/jx-test/pkg/dynkube"
	"github.com/jenkins-x-plugins/jx-test/pkg/terraforms"
	"github.com/jenkins-x/jx-helpers/v3/pkg/cmdrunner"
	"github.com/jenkins-x/jx-helpers/v3/pkg/termcolor"
	"k8s.io/client-go/kubernetes"
	"strings"
	"time"

	"github.com/jenkins-x-plugins/jx-test/pkg/root"
	"github.com/jenkins-x/jx-helpers/v3/pkg/cobras/helper"
	"github.com/jenkins-x/jx-helpers/v3/pkg/cobras/templates"
	"github.com/jenkins-x/jx-helpers/v3/pkg/kube"
	"github.com/jenkins-x/jx-logging/v3/pkg/log"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
)

var (
	info = termcolor.ColorInfo

	cmdLong = templates.LongDesc(`
		Garbage collects test resources
`)

	cmdExample = templates.Examples(`
		%s gc
	`)
)

// Options the options for the command
type Options struct {
	Selector      string
	Namespace     string
	Duration      time.Duration
	KubeClient    kubernetes.Interface
	DynamicClient dynamic.Interface
	Ctx           context.Context
	Client        dynamic.ResourceInterface
	CommandRunner cmdrunner.CommandRunner
}

// NewCmdGC creates a command object for the command
func NewCmdGC() (*cobra.Command, *Options) {
	o := &Options{}

	cmd := &cobra.Command{
		Use:     "gc",
		Short:   "Garbage collects test resources",
		Long:    cmdLong,
		Example: fmt.Sprintf(cmdExample, root.BinaryName),
		Run: func(cmd *cobra.Command, args []string) {
			err := o.Run()
			helper.CheckErr(err)
		},
	}

	if o.Ctx == nil {
		o.Ctx = cmd.Context()
	}

	cmd.Flags().StringVarP(&o.Namespace, "ns", "n", "", "the namespace to query the Terraform resources")
	cmd.Flags().StringVarP(&o.Selector, "selector", "l", "kind="+terraforms.LabelValueKindTest, "the selector to find the Terraform resources to remove")
	cmd.Flags().DurationVarP(&o.Duration, "duration", "d", 2*time.Hour, "The maximum age of a Terraform resource before it is garbage collected")
	return cmd, o
}

// Run implements the command
func (o *Options) Run() error {
	err := o.Validate()
	if err != nil {
		return errors.Wrapf(err, "failed to validate setup")
	}

	ctx := o.GetContext()
	ns := o.Namespace
	gvr := terraforms.TerraformResource
	o.Client = dynkube.DynamicResource(o.DynamicClient, ns, gvr)

	kind := strings.Title(strings.TrimSuffix(gvr.Resource, "s"))

	// lets delete all the previous resources for this Pull Request and Context
	list, err := o.Client.List(ctx, metav1.ListOptions{
		LabelSelector: o.Selector,
	})
	if err != nil && apierrors.IsNotFound(err) {
		return errors.Wrapf(err, "could not find resources for ")
	}

	createdBefore := time.Now().Add(o.Duration * -1)
	createdTime := &metav1.Time{
		Time: createdBefore,
	}
	for _, r := range list.Items {
		name := r.GetName()

		labels := r.GetLabels()
		if labels != nil {
			keep := labels["keep"]
			if keep != "" {
				log.Logger().Infof("not removing %s %s as it has a keep label", kind, info(name))
				continue
			}
		}

		created := r.GetCreationTimestamp()
		if !created.Before(createdTime) {
			log.Logger().Infof("not removing %s %s as it was created at %s", kind, info(name), created.String())
			continue
		}

		err = o.deleteTerraform(ctx, kind, name)
		if err != nil {
			return errors.Wrapf(err, "failed to delete %s %s", kind, name)
		}

		log.Logger().Infof("deleted %s %s as it was created at: %s", kind, info(name), created.String())
	}
	return nil
}

func (o *Options) deleteTerraform(ctx context.Context, kind, name string) error {
	ns := o.Namespace
	err := terraforms.DeleteActiveTerraformJobs(ctx, o.KubeClient, ns, name)
	if err != nil {
		return errors.Wrapf(err, "failed to delete active Terraform Jobs for namespace %s name %s", ns, name)
	}

	log.Logger().Infof("deleting %s %s", kind, info(name))
	c := &cmdrunner.Command{
		Name: "kubectl",
		Args: []string{"delete", kind, name},
	}
	_, err = o.CommandRunner(c)
	if err != nil {
		return errors.Wrapf(err, "failed to run %s", c.CLI())
	}
	return nil
}

func (o *Options) Validate() error {
	if o.CommandRunner == nil {
		o.CommandRunner = cmdrunner.QuietCommandRunner
	}
	var err error
	o.KubeClient, o.Namespace, err = kube.LazyCreateKubeClientAndNamespace(o.KubeClient, o.Namespace)
	if err != nil {
		return errors.Wrapf(err, "failed to create kube client")
	}
	o.DynamicClient, err = kube.LazyCreateDynamicClient(o.DynamicClient)
	if err != nil {
		return errors.Wrapf(err, "failed to craete dynamic client")
	}
	return nil
}

// GetContext lazily creates a context if it doesn't exist already
func (o *Options) GetContext() context.Context {
	if o.Ctx == nil {
		o.Ctx = context.TODO()
	}
	return o.Ctx
}
