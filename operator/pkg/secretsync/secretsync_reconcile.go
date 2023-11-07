// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package secretsync

import (
	"context"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	controllerruntime "github.com/cilium/cilium/operator/pkg/controller-runtime"
	"github.com/cilium/cilium/pkg/logging/logfields"
)

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *secretSyncer) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	scopedLog := r.logger.WithFields(logrus.Fields{
		logfields.Controller: "secret-syncer",
		logfields.Resource:   req.NamespacedName,
	})
	scopedLog.Info("Syncing secrets")

	original := &corev1.Secret{}
	if err := r.client.Get(ctx, req.NamespacedName, original); err != nil {
		if k8serrors.IsNotFound(err) {
			scopedLog.WithError(err).Debug("Unable to get Secret - either deleted or not yet available")

			// Check if there's an existing synced secret for the deleted Secret
			if err := r.cleanupSyncedSecret(ctx, req, scopedLog); err != nil {
				return controllerruntime.Fail(err)
			}

			return controllerruntime.Success()
		}

		return controllerruntime.Fail(err)
	}

	if !r.mainObjectReferencedFunc(ctx, r.client, original) {
		// Check if there's an existing synced secret that should be deleted
		if err := r.cleanupSyncedSecret(ctx, req, scopedLog); err != nil {
			return controllerruntime.Fail(err)
		}
		return controllerruntime.Success()
	}

	desiredSync := desiredSyncSecret(r.secretsNamespace, original)

	if err := r.ensureSyncedSecret(ctx, desiredSync); err != nil {
		return controllerruntime.Fail(err)
	}

	scopedLog.Info("Successfully synced secrets")
	return controllerruntime.Success()
}

func (r *secretSyncer) cleanupSyncedSecret(ctx context.Context, req reconcile.Request, scopedLog *logrus.Entry) error {
	syncSecret := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{Namespace: r.secretsNamespace, Name: req.Namespace + "-" + req.Name}, syncSecret); err == nil {
		// Try to delete existing synced secret
		scopedLog.Debug("Delete synced secret")
		if err := r.client.Delete(ctx, syncSecret); err != nil {
			return err
		}
	}

	return nil
}

func desiredSyncSecret(secretsNamespace string, original *corev1.Secret) *corev1.Secret {
	s := &corev1.Secret{}
	s.SetNamespace(secretsNamespace)
	s.SetName(original.Namespace + "-" + original.Name)
	s.SetAnnotations(original.GetAnnotations())
	s.SetLabels(original.GetLabels())
	if s.Labels == nil {
		s.Labels = map[string]string{}
	}
	s.Labels[OwningSecretNamespace] = original.Namespace
	s.Labels[OwningSecretName] = original.Name
	s.Immutable = original.Immutable
	s.Data = original.Data
	s.StringData = original.StringData
	s.Type = original.Type

	return s
}

func (r *secretSyncer) ensureSyncedSecret(ctx context.Context, desired *corev1.Secret) error {
	existing := &corev1.Secret{}
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(desired), existing); err != nil {
		if k8serrors.IsNotFound(err) {
			return r.client.Create(ctx, desired)
		}
		return err
	}

	temp := existing.DeepCopy()
	temp.SetAnnotations(desired.GetAnnotations())
	temp.SetLabels(desired.GetLabels())
	temp.Immutable = desired.Immutable
	temp.Data = desired.Data
	temp.StringData = desired.StringData
	temp.Type = desired.Type

	return r.client.Patch(ctx, temp, client.MergeFrom(existing))
}