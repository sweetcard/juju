// Copyright 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"fmt"
	"strings"

	"github.com/juju/cmd"
	"github.com/juju/errors"
	"github.com/juju/names"
	"launchpad.net/gnuflag"

	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/cmd/envcmd"
	"github.com/juju/juju/constraints"
	"github.com/juju/juju/environs/config"
	"github.com/juju/juju/environs/configstore"
	"github.com/juju/juju/environs/manual"
	"github.com/juju/juju/instance"
	"github.com/juju/juju/provider"
)

// sshHostPrefix is the prefix for a machine to be "manually provisioned".
const sshHostPrefix = "ssh:"

var addMachineDoc = `

Juju supports adding machines using provider-specific machine instances
(EC2 instances, OpenStack servers, MAAS nodes, etc.); existing machines
running a supported operating system (see "manual provisioning" below),
and containers on machines. Machines are created in a clean state and
ready to have units deployed.

Without any parameters, add-machine will allocate a new provider-specific
machine (multiple, if "-n" is provided). When adding a new machine, you
may specify constraints for the machine to be provisioned; the provider
will interpret these constraints in order to decide what kind of machine
to allocate.

If a container type is specified (e.g. "lxc"), then add-machine will
allocate a container of that type on a new provider-specific machine. It is
also possible to add containers to existing machines using the format
<container type>:<machine number>. Constraints cannot be combined with
deploying a container to an existing machine. The currently supported
container types are: $CONTAINER_TYPES$.

Manual provisioning is the process of installing Juju on an existing machine
and bringing it under Juju's management; currently this requires that the
machine be running Ubuntu, that it be accessible via SSH, and be running on
the same network as the API server.

It is possible to override or augment constraints by passing provider-specific
"placement directives" with "--to"; these give the provider additional
information about how to allocate the machine. For example, one can direct the
MAAS provider to acquire a particular node by specifying its hostname with
"--to". For more information on placement directives, see "juju help placement".

Examples:
   juju add-machine                      (starts a new machine)
   juju add-machine -n 2                 (starts 2 new machines)
   juju add-machine lxc                  (starts a new machine with an lxc container)
   juju add-machine lxc -n 2             (starts 2 new machines with an lxc container)
   juju add-machine lxc:4                (starts a new lxc container on machine 4)
   juju add-machine --constraints mem=8G (starts a machine with at least 8GB RAM)
   juju add-machine ssh:user@10.10.0.3   (manually provisions a machine with ssh)
   juju add-machine zone=us-east-1a

See Also:
   juju help constraints
   juju help placement
`

func init() {
	containerTypes := make([]string, len(instance.ContainerTypes))
	for i, t := range instance.ContainerTypes {
		containerTypes[i] = string(t)
	}
	addMachineDoc = strings.Replace(
		addMachineDoc,
		"$CONTAINER_TYPES$",
		strings.Join(containerTypes, ", "),
		-1,
	)
}

// AddMachineCommand starts a new machine and registers it in the environment.
type AddMachineCommand struct {
	envcmd.EnvCommandBase
	// If specified, use this series, else use the environment default-series
	Series string
	// If specified, these constraints are merged with those already in the environment.
	Constraints constraints.Value
	// Placement is passed verbatim to the API, to be parsed and evaluated server-side.
	Placement *instance.Placement

	NumMachines int
}

func (c *AddMachineCommand) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "add-machine",
		Args:    "[<container>:machine | <container> | ssh:[user@]host | placement]",
		Purpose: "start a new, empty machine and optionally a container, or add a container to a machine",
		Doc:     addMachineDoc,
	}
}

func (c *AddMachineCommand) SetFlags(f *gnuflag.FlagSet) {
	f.StringVar(&c.Series, "series", "", "the charm series")
	f.IntVar(&c.NumMachines, "n", 1, "The number of machines to add")
	f.Var(constraints.ConstraintsValue{Target: &c.Constraints}, "constraints", "additional machine constraints")
}

func (c *AddMachineCommand) Init(args []string) error {
	if c.Constraints.Container != nil {
		return fmt.Errorf("container constraint %q not allowed when adding a machine", *c.Constraints.Container)
	}
	placement, err := cmd.ZeroOrOneArgs(args)
	if err != nil {
		return err
	}
	c.Placement, err = instance.ParsePlacement(placement)
	if err == instance.ErrPlacementScopeMissing {
		placement = "env-uuid" + ":" + placement
		c.Placement, err = instance.ParsePlacement(placement)
	}
	if err != nil {
		return err
	}
	if c.NumMachines > 1 && c.Placement != nil && c.Placement.Directive != "" {
		return fmt.Errorf("cannot use -n when specifying a placement directive")
	}
	return nil
}

type addMachineAPI interface {
	AddMachines([]params.AddMachineParams) ([]params.AddMachinesResult, error)
	AddMachines1dot18([]params.AddMachineParams) ([]params.AddMachinesResult, error)
	Close() error
	ForceDestroyMachines(machines ...string) error
	EnvironmentUUID() string
	ProvisioningScript(params.ProvisioningScriptParams) (script string, err error)
}

var getAddMachineAPI = func(c *AddMachineCommand) (addMachineAPI, error) {
	return c.NewAPIClient()
}

var manualProvisioner = manual.ProvisionMachine

func (c *AddMachineCommand) Run(ctx *cmd.Context) error {
	client, err := getAddMachineAPI(c)
	if err != nil {
		return errors.Trace(err)
	}
	defer client.Close()

	var config *config.Config
	if defaultStore, err := configstore.Default(); err != nil {
		return err
	} else if config, err = c.Config(defaultStore); err != nil {
		return err
	}

	if c.Placement != nil && c.Placement.Scope == "ssh" {
		// Manual provisioning.
		args := manual.ProvisionMachineArgs{
			Host:   c.Placement.Directive,
			Client: client,
			Stdin:  ctx.Stdin,
			Stdout: ctx.Stdout,
			Stderr: ctx.Stderr,
			UpdateBehavior: &params.UpdateBehavior{
				config.EnableOSRefreshUpdate(),
				config.EnableOSUpgrade(),
			},
		}
		machineId, err := manualProvisioner(args)
		if err == nil {
			ctx.Infof("created machine %v", machineId)
		}
		return err
	}

	if c.Placement != nil && c.Placement.Scope == "env-uuid" {
		c.Placement.Scope = client.EnvironmentUUID()
	}

	if c.Placement != nil && c.Placement.Scope == instance.MachineScope {
		// It does not make sense to add-machine <id>.
		return fmt.Errorf("machine-id cannot be specified when adding machines")
	}

	jobs := []params.MachineJob{params.JobHostUnits}
	if config.Type() != provider.MAAS {
		// In case of MAAS JobManageNetworking is not added to ensure
		// the non-intrusive start of a networker like above for the
		// manual provisioning.
		jobs = append(jobs, params.JobManageNetworking)
	}

	machineParams := params.AddMachineParams{
		Placement:   c.Placement,
		Series:      c.Series,
		Constraints: c.Constraints,
		Jobs:        jobs,
	}
	machines := make([]params.AddMachineParams, c.NumMachines)
	for i := 0; i < c.NumMachines; i++ {
		machines[i] = machineParams
	}

	results, err := client.AddMachines(machines)
	if params.IsCodeNotImplemented(err) {
		if c.Placement != nil {
			containerType, parseErr := instance.ParseContainerType(c.Placement.Scope)
			if parseErr != nil {
				// The user specified a non-container placement directive:
				// return original API not implemented error.
				return err
			}
			machineParams.ContainerType = containerType
			machineParams.ParentId = c.Placement.Directive
			machineParams.Placement = nil
		}
		logger.Infof(
			"AddMachinesWithPlacement not supported by the API server, " +
				"falling back to 1.18 compatibility mode",
		)
		results, err = client.AddMachines1dot18([]params.AddMachineParams{machineParams})
	}
	if err != nil {
		return errors.Trace(err)
	}

	errs := []error{}
	for _, machineInfo := range results {
		if machineInfo.Error != nil {
			errs = append(errs, machineInfo.Error)
			continue
		}
		machineId := machineInfo.Machine

		if names.IsContainerMachine(machineId) {
			ctx.Infof("created container %v", machineId)
		} else {
			ctx.Infof("created machine %v", machineId)
		}
	}
	if len(errs) == 1 {
		fmt.Fprintf(ctx.Stderr, "failed to create 1 machine\n")
		return errs[0]
	}
	if len(errs) > 1 {
		fmt.Fprintf(ctx.Stderr, "failed to create %d machines\n", len(errs))
		returnErr := []string{}
		for _, e := range errs {
			returnErr = append(returnErr, fmt.Sprintf("%s", e))
		}
		return errors.New(strings.Join(returnErr, ", "))
	}
	return nil
}
