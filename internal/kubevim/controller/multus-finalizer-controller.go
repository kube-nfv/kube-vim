package controller

// MultusFinalizerController manages finalizers on NetworkAttachmentDefinitions to prevent
// premature deletion while VirtualMachines are still using the network interfaces.
//
// Problem this controller solves:
// When a network resource is deleted before the VM that uses it, VM deletion fails because
// it tries to resolve network information from the already-deleted NetworkAttachmentDefinition.
// This creates a race condition where the deletion order matters.
//
// Controller behavior:
// 1. Watches NetworkAttachmentDefinition resources managed by kube-vim
// 2. Watches VirtualMachine resources that reference these NetworkAttachmentDefinitions
// 3. Adds finalizers to NetworkAttachmentDefinitions when VMs are using them
// 4. Blocks NetworkAttachmentDefinition deletion until all referencing VMs are deleted
// 5. Removes finalizers when no VMs are using the network attachment anymore
//
// This ensures proper cleanup order: VMs are always deleted before their network dependencies,
// preventing the race condition and allowing clean resource cleanup.
//
// Example flow:
// 1. VM created with multus network interface → Finalizer added to NetworkAttachmentDefinition
// 2. Network deletion requested → Blocked by finalizer while VM exists
// 3. VM deleted → Finalizer removed from NetworkAttachmentDefinition
// 4. NetworkAttachmentDefinition deletion proceeds → Clean cleanup
//
// Controller watches:
// - NetworkAttachmentDefinitions (primary resource)
// - VirtualMachines (secondary resource for dependency tracking)
//
// Finalizer name: "vm-dependency.multus.kubevim.kubenfv.io"

import (
	"context"
	"fmt"

	logger "go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// MultusVMDependencyFinalizer is added to NetworkAttachmentDefinitions
	// to prevent deletion while VirtualMachines are still using them
	MultusVMDependencyFinalizer = "vm-dependency.multus.kubevim.kubenfv.io"

	// ControllerName for logging and metrics
	ControllerName = "multus-finalizer-controller"
)

type MultusFinalizerReconciler struct {
	client               client.Client
	log                  logger.Logger
}

func (r *MultusFinalizerReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func NewMultusFinalizerController(ctx context.Context, mgr manager.Manager, log logger.Logger) (controller.Controller, error) {
	client := mgr.GetClient()
	reconciler := &MultusFinalizerReconciler{
		client: client,
		log: *log.Named(ControllerName),
	}

	multusFinController, err := controller.New(ControllerName, mgr, controller.Options{
		MaxConcurrentReconciles: 3,
		Reconciler:              reconciler,
	})
	if err != nil {
		return nil, fmt.Errorf("controller \"%s\" create: %w", ControllerName, err)
	}
	if err := addMultusFinalizerControllerWatchers(); err != nil {
		return nil, fmt.Errorf("add watches for \"%s\" controller: %w", ControllerName, err)
	}
	return multusFinController, nil
}

func addMultusFinalizerControllerWatchers() error {
	// Add watch for multus resources, kubevirt vmi resources. ?? VM resources?
	return nil
}
