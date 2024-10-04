package parser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/alitto/pond"
	v1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/go-logr/logr"
	"github.com/upbound/provider-terraform/apis/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

type Parser struct {
	Out          string
	AllowFailure bool
	FailFast     bool
	Workers      int
	Decoder      runtime.Decoder
	Logger       logr.Logger
}

func (p *Parser) Run(ctx context.Context, in io.Reader) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error)

	panicHandler := func(panic interface{}) {
		errs <- fmt.Errorf("worker exits from a panic: %v\nStack trace: %s", panic, string(debug.Stack()))
	}

	pool := pond.New(p.Workers, p.Workers, pond.Context(ctx), pond.PanicHandler(panicHandler))
	outWriter := pond.New(1, 1, pond.Context(ctx), pond.PanicHandler(panicHandler))

	objects := make(chan runtime.Object, p.Workers)
	index := make(ResourceIndex)

	outWriter.Submit(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case obj, ok := <-objects:
				if !ok {
					return
				}

				if err := index.Push(obj); err != nil {
					errs <- err
					return
				}
			}
		}
	})

	var lastErr error
	defer func() {
		if lastErr != nil && !p.AllowFailure {
			fmt.Fprintln(os.Stderr, lastErr.Error())
			os.Exit(1)
		}
	}()

	go func() {
		for err := range errs {
			if err == nil {
				continue
			}

			lastErr = err

			if p.FailFast {
				cancel()
			}
		}
	}()

	multidocReader := utilyaml.NewYAMLReader(bufio.NewReader(in))

	for {
		resourceYAML, err := multidocReader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}

			return err
		}

		pool.Submit(func() {
			obj, gvk, err := p.Decoder.Decode(
				resourceYAML,
				nil,
				nil)
			if err != nil {
				return
			}

			if gvk.Group == v1beta1.Group && gvk.Kind == "ProviderConfig" {
				_ = accessor.SetNamespace(obj, "")
			}

			objects <- obj
		})
	}

	pool.StopAndWait()
	close(objects)
	outWriter.StopAndWait()

	var moduleIndex []string

	for ref, obj := range index {
		switch {
		case ref.Group == v1beta1.Group && ref.Kind == "Workspace":
			ws := obj.(*v1beta1.Workspace)
			varsWorkspace, err := p.handleWorkspace(p.Out, ws, index)
			if err != nil {
				return err
			}

			var vars []string
			for k, v := range varsWorkspace {
				vars = append(vars, fmt.Sprintf("%s=\"%s\"", k, v))
			}

			moduleIndex = append(moduleIndex, fmt.Sprintf("module \"%s\" {\n  source = \"./%s\"\n%s\n}", ws.Name, ws.Name, strings.Join(vars, "\n")))
		}
	}

	return os.WriteFile(filepath.Join(p.Out, "main.tf"), []byte(strings.Join(moduleIndex, "\n")), 0640)
}

func (p *Parser) getProvider(name string, resources ResourceIndex) (*v1beta1.ProviderConfig, error) {
	ref := ref{
		GroupKind: schema.GroupKind{
			Group: v1beta1.Group,
			Kind:  "ProviderConfig",
		},
		Name: name,
	}

	if obj, ok := resources[ref]; ok {
		return obj.(*v1beta1.ProviderConfig), nil
	}

	return nil, fmt.Errorf("no provider config `%s`", name)
}

func (p *Parser) getProviderVars(pc *v1beta1.ProviderConfig, resources ResourceIndex) (map[string]string, error) {
	vars := make(map[string]string)
	for _, vf := range pc.Spec.Credentials {
		switch vf.Source {
		case v1.CredentialsSourceSecret:
			ref := ref{
				GroupKind: schema.GroupKind{
					Group: "",
					Kind:  "Secret",
				},
				Name:      vf.SecretRef.Name,
				Namespace: vf.SecretRef.Namespace,
			}

			if obj, ok := resources[ref]; ok {
				secret := obj.(*corev1.Secret)
				for k, v := range secret.StringData {
					vars[k] = v
				}
			} else {
				return vars, fmt.Errorf("could not find providerconfig secret: %#v", ref)
			}
		}
	}

	return vars, nil
}

func (p *Parser) handleWorkspace(tfDir string, ws *v1beta1.Workspace, resources ResourceIndex) (map[string]string, error) {
	pc, err := p.getProvider(ws.GetProviderConfigReference().Name, resources)
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(tfDir, ws.Name)
	if err := os.MkdirAll(dir, 0700); resource.Ignore(os.IsExist, err) != nil {
		return nil, err
	}

	switch ws.Spec.ForProvider.Source {
	case v1beta1.ModuleSourceRemote:
		return nil, fmt.Errorf("unsupported provider source: %s", v1beta1.ModuleSourceRemote)
	case v1beta1.ModuleSourceInline:
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(ws.Spec.ForProvider.Module), 0600); err != nil {
			return nil, err
		}
	}

	vars, err := p.getProviderVars(pc, resources)

	if err != nil {
		return nil, err
	}

	if len(ws.Spec.ForProvider.Entrypoint) > 0 {
		entrypoint := strings.ReplaceAll(ws.Spec.ForProvider.Entrypoint, "../", "")
		dir = filepath.Join(dir, entrypoint)
	}

	if pc.Spec.Configuration != nil {
		if err := os.WriteFile(filepath.Join(dir, "crossplane-provider-config.tf"), []byte(*pc.Spec.Configuration), 0600); err != nil {
			return nil, err
		}
	}

	if pc.Spec.BackendFile != nil {
		if err := os.WriteFile(filepath.Join(dir, "backend.tf"), []byte(*pc.Spec.BackendFile), 0600); err != nil {
			return nil, err
		}
	}

	for _, env := range ws.Spec.ForProvider.Env {
		varName, isTFEnv := strings.CutPrefix(env.Name, "TF_VAR_")
		if !isTFEnv {
			continue
		}

		switch {
		case env.Value != "":
		case env.ConfigMapKeyReference != nil:
			ref := ref{
				GroupKind: schema.GroupKind{
					Group: "",
					Kind:  "ConfigMap",
				},
				Name:      env.SecretKeyReference.Name,
				Namespace: env.SecretKeyReference.Namespace,
			}

			if obj, ok := resources[ref]; ok {
				cm := obj.(*corev1.ConfigMap)
				if cmValue, ok := cm.Data[env.SecretKeyReference.Key]; ok {
					vars[varName] = cmValue
				} else {
					return nil, fmt.Errorf("could not find referenced key %s in configmap: %#v", env.SecretKeyReference.Key, ref)
				}
			} else {
				return nil, fmt.Errorf("could not find referenced secret: %#v", ref)
			}

			if obj, ok := resources[ref]; ok {
				cm := obj.(*corev1.ConfigMap)
				for k, v := range cm.Data {
					vars[k] = v
				}
			} else {
				return nil, fmt.Errorf("could not find referenced configmap: %#v", ref)
			}

		case env.SecretKeyReference != nil:
			ref := ref{
				GroupKind: schema.GroupKind{
					Group: "",
					Kind:  "Secret",
				},
				Name:      env.SecretKeyReference.Name,
				Namespace: env.SecretKeyReference.Namespace,
			}

			if obj, ok := resources[ref]; ok {
				secret := obj.(*corev1.Secret)
				if secretValue, ok := secret.StringData[env.SecretKeyReference.Key]; ok {
					vars[varName] = secretValue
				} else {
					return nil, fmt.Errorf("could not find referenced key %s in secret: %#v", env.SecretKeyReference.Key, ref)
				}
			} else {
				return nil, fmt.Errorf("could not find referenced secret: %#v", ref)
			}

		default:
			return nil, errors.New("unsupported env mechanism")
		}
	}

	if len(vars) > 0 {
		b, err := json.MarshalIndent(vars, "", " ")
		if err != nil {
			return nil, err
		}

		if err := os.WriteFile(filepath.Join(dir, "terraform.tfvars.json"), b, 0600); err != nil {
			return nil, err
		}
	}

	return vars, nil
}
