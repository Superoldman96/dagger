package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/spf13/pflag"

	"dagger.io/dagger"
)

type UnsupportedFlagError struct {
	Name string
	Type string
}

func (e *UnsupportedFlagError) Error() string {
	msg := fmt.Sprintf("unsupported type for flag --%s", e.Name)
	if e.Type != "" {
		msg = fmt.Sprintf("%s: %s", msg, e.Type)
	}
	return msg
}

// GetCustomFlagValue returns a pflag.Value instance for a dagger.ObjectTypeDef name.
func GetCustomFlagValue(name string) DaggerValue {
	switch name {
	case Container:
		return &containerValue{}
	case Directory:
		return &directoryValue{}
	case File:
		return &fileValue{}
	case Secret:
		return &secretValue{}
	case Service:
		return &serviceValue{}
	case PortForward:
		return &portForwardValue{}
	case CacheVolume:
		return &cacheVolumeValue{}
	case ModuleSource:
		return &moduleSourceValue{}
	case Module:
		return &moduleValue{}
	case Platform:
		return &platformValue{}
	case BuildArg:
		return &buildArgValue{}
	case Socket:
		return &socketValue{}
	case GitRepository:
		return &gitRepositoryValue{}
	case GitRef:
		return &gitRefValue{}
	}
	return nil
}

// GetCustomFlagValueSlice returns a pflag.Value instance for a dagger.ObjectTypeDef name.
func GetCustomFlagValueSlice(name string, defVal []string) (DaggerValue, error) {
	switch name {
	case Container:
		v := &sliceValue[*containerValue]{}
		return v.SetDefault(defVal)
	case Directory:
		v := &sliceValue[*directoryValue]{}
		return v.SetDefault(defVal)
	case File:
		v := &sliceValue[*fileValue]{}
		return v.SetDefault(defVal)
	case Secret:
		v := &sliceValue[*secretValue]{}
		return v.SetDefault(defVal)
	case Service:
		v := &sliceValue[*serviceValue]{}
		return v.SetDefault(defVal)
	case PortForward:
		v := &sliceValue[*portForwardValue]{}
		return v.SetDefault(defVal)
	case CacheVolume:
		v := &sliceValue[*cacheVolumeValue]{}
		return v.SetDefault(defVal)
	case ModuleSource:
		v := &sliceValue[*moduleSourceValue]{}
		return v.SetDefault(defVal)
	case Module:
		v := &sliceValue[*moduleValue]{}
		return v.SetDefault(defVal)
	case Platform:
		v := &sliceValue[*platformValue]{}
		return v.SetDefault(defVal)
	case BuildArg:
		v := &sliceValue[*buildArgValue]{}
		return v.SetDefault(defVal)
	case Socket:
		v := &sliceValue[*socketValue]{}
		return v.SetDefault(defVal)
	}
	return nil, nil
}

// DaggerValue is a pflag.Value that requires a dagger.Client for producing the
// final value.
type DaggerValue interface {
	pflag.Value

	// Get returns the final value for the query builder.
	Get(context.Context, *dagger.Client, *dagger.ModuleSource, *modFunctionArg) (any, error)
}

// sliceValue is a pflag.Value that builds a slice of DaggerValue instances.
//
// NOTE: the code defining this type is heavily inspired by stringSliceValue.Set
// for equivalent behaviour as the other builtin slice types
type sliceValue[T DaggerValue] struct {
	value   []T
	changed bool
	Init    func() T
}

func (v *sliceValue[T]) Type() string {
	var t T
	if v.Init != nil {
		t = v.Init()
	}
	return "[]" + t.Type()
}

func (v *sliceValue[T]) String() string {
	ss := []string{}
	for _, v := range v.value {
		ss = append(ss, v.String())
	}
	out, _ := writeAsCSV(ss)
	return "[" + out + "]"
}

func (v *sliceValue[T]) Get(ctx context.Context, c *dagger.Client, modSrc *dagger.ModuleSource, modArg *modFunctionArg) (any, error) {
	out := make([]any, len(v.value))
	for i, v := range v.value {
		outV, err := v.Get(ctx, c, modSrc, modArg)
		if err != nil {
			return nil, err
		}
		out[i] = outV
	}
	return out, nil
}

func (v *sliceValue[T]) SetDefault(s []string) (*sliceValue[T], error) {
	if s == nil {
		return v, nil
	}
	if err := v.Set(strings.Join(s, ",")); err != nil {
		return v, err
	}
	v.changed = false
	return v, nil
}

func (v *sliceValue[T]) Set(s string) error {
	// remove all quote characters
	rmQuote := strings.NewReplacer(`"`, "", `'`, "", "`", "")

	// read flag arguments with CSV parser
	ss, err := readAsCSV(rmQuote.Replace(s))
	if err != nil && err != io.EOF {
		return err
	}

	// parse values into slice
	out := make([]T, 0, len(ss))
	for _, s := range ss {
		var vv T

		if v.Init != nil {
			vv = v.Init()
		} else {
			if typ := reflect.TypeOf(vv); typ.Kind() == reflect.Ptr {
				// hack to get a pointer to a new instance of the underlying type
				vv = reflect.New(typ.Elem()).Interface().(T)
			}
		}

		if err := vv.Set(strings.TrimSpace(s)); err != nil {
			return err
		}
		out = append(out, vv)
	}

	if !v.changed {
		v.value = out
	} else {
		v.value = append(v.value, out...)
	}

	v.changed = true
	return nil
}

func newEnumSliceValue(typedef *modEnum, defaultValues []string) *sliceValue[*enumValue] {
	v := &sliceValue[*enumValue]{
		Init: func() *enumValue {
			return newEnumValue(typedef, "")
		},
	}
	for _, defaultValue := range defaultValues {
		v.value = append(v.value, newEnumValue(typedef, defaultValue))
	}
	return v
}

func newEnumValue(typedef *modEnum, defaultValue string) *enumValue {
	v := &enumValue{typedef: typedef}
	v.value = defaultValue
	return v
}

type enumValue struct {
	value   string
	typedef *modEnum
}

var _ DaggerValue = &enumValue{}

func (v *enumValue) Type() string {
	vs := make([]string, 0, len(v.typedef.Members))
	for _, v := range v.typedef.Members {
		vs = append(vs, v.Name)
	}
	return strings.Join(vs, ",")
}

func (v *enumValue) String() string {
	return v.value
}

func (v *enumValue) Get(ctx context.Context, dag *dagger.Client, modSrc *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	return v.value, nil
}

func (v *enumValue) Set(s string) error {
	for _, allow := range v.typedef.Members {
		if strings.EqualFold(s, allow.Name) {
			v.value = allow.Name
			return nil
		}
	}

	return fmt.Errorf("value should be one of %s", v.Type())
}

// containerValue is a pflag.Value that builds a dagger.Container from a
// base image name.
type containerValue struct {
	address string
}

func (v *containerValue) Type() string {
	return Container
}

func (v *containerValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("container address cannot be empty")
	}
	v.address = s
	return nil
}

func (v *containerValue) String() string {
	return v.address
}

func (v *containerValue) Get(ctx context.Context, c *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	if v.address == "" {
		return nil, fmt.Errorf("container address cannot be empty")
	}
	return c.Container().From(v.String()).Sync(ctx)
}

// directoryValue is a pflag.Value that builds a dagger.Directory from a host path.
type directoryValue struct {
	address string
}

func (v *directoryValue) Type() string {
	return Directory
}

func (v *directoryValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("directory address cannot be empty")
	}
	v.address = s
	return nil
}

func (v *directoryValue) String() string {
	return v.address
}

func (v *directoryValue) Get(ctx context.Context, dag *dagger.Client, modSrc *dagger.ModuleSource, modArg *modFunctionArg) (any, error) {
	if v.String() == "" {
		return nil, fmt.Errorf("directory address cannot be empty")
	}

	// Try parsing as a Git URL
	gitURL, err := parseGitURL(v.String())
	if err == nil {
		return dag.Directory().
			WithDirectory(
				"/",
				makeGitDirectory(gitURL, dag),
				dagger.DirectoryWithDirectoryOpts{
					Exclude: modArg.Ignore,
				}).
			Sync(ctx)
	}

	// Otherwise it's a local dir path
	path := v.String()
	path, err = getLocalPath(path)
	if err != nil {
		return nil, err
	}

	return dag.Host().Directory(path, dagger.HostDirectoryOpts{
		Exclude: modArg.Ignore,
	}).Sync(ctx)
}

// makeGitDirectory creates a dagger.Directory object from a parsed gitutil.GitURL
func makeGitDirectory(gitURL *gitutil.GitURL, dag *dagger.Client) *dagger.Directory {
	gitOpts := dagger.GitOpts{
		KeepGitDir: true,
	}
	git := dag.Git(gitURL.Remote(), gitOpts)
	var gitRef *dagger.GitRef
	if gitURL.Fragment.Ref == "" {
		gitRef = git.Head()
	} else {
		gitRef = git.Ref(gitURL.Fragment.Ref)
	}
	gitDir := gitRef.Tree()
	if subdir := gitURL.Fragment.Subdir; subdir != "" {
		gitDir = gitDir.Directory(subdir)
	}
	return gitDir
}

func parseGitURL(url string) (*gitutil.GitURL, error) {
	// FIXME: handle tarball-over-http (where http(s):// is scheme but not a git repo)
	u, err := gitutil.ParseURL(url)
	if err != nil {
		return nil, err
	}
	// TODO: default scheme?
	if u.Fragment == nil {
		u.Fragment = &gitutil.GitURLFragment{}
	}
	return u, nil
}

// fileValue is a pflag.Value that builds a dagger.File from a host path.
type fileValue struct {
	path string
}

func (v *fileValue) Type() string {
	return File
}

func (v *fileValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("file path cannot be empty")
	}
	v.path = s
	return nil
}

func (v *fileValue) String() string {
	return v.path
}

func (v *fileValue) Get(ctx context.Context, dag *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	if v.String() == "" {
		return nil, fmt.Errorf("file path cannot be empty")
	}

	// Try parsing as a Git URL
	parsedGit, err := parseGitURL(v.String())
	if err == nil {
		gitOpts := dagger.GitOpts{
			KeepGitDir: true,
		}
		git := dag.Git(parsedGit.Remote(), gitOpts)
		var gitRef *dagger.GitRef
		if parsedGit.Fragment.Ref == "" {
			gitRef = git.Head()
		} else {
			gitRef = git.Branch(parsedGit.Fragment.Ref)
		}
		gitDir := gitRef.Tree()
		path := parsedGit.Fragment.Subdir
		if path == "" {
			return nil, fmt.Errorf("expected path selection for git repo")
		}
		return gitDir.File(path).Sync(ctx)
	}

	// Otherwise it's a local file path
	path := v.String()
	path, err = getLocalPath(path)
	if err != nil {
		return nil, err
	}

	return dag.Host().File(path).Sync(ctx)
}

// secretValue is a pflag.Value that builds a dagger.Secret from a name and a
// plaintext value.
type secretValue struct {
	uri      string
	cacheKey string
}

func (v *secretValue) Type() string {
	return Secret
}

func (v *secretValue) Set(s string) error {
	if !strings.Contains(s, ":") {
		// case of e.g. `--token MY_ENV_SECRET`, which is shorthand for `--token env://MY_ENV_SECRET`
		s = "env://" + s
	}
	// legacy secrets in the form of `--token env:MY_ENV_SECRET` instead of `env://MY_ENV_SECRET`
	secretSource, val, _ := strings.Cut(s, ":")
	if !strings.HasPrefix(val, "//") {
		s = secretSource + "://" + val
	}

	sWithoutQuery, queryValsStr, ok := strings.Cut(s, "?")
	if ok && len(queryValsStr) > 0 {
		queryVals, err := url.ParseQuery(queryValsStr)
		if err != nil {
			return err
		}
		if cacheKey := queryVals.Get("cacheKey"); cacheKey != "" {
			v.cacheKey = cacheKey
			queryVals.Del("cacheKey")
			queryValsStr = queryVals.Encode()
			if len(queryValsStr) > 0 {
				s = fmt.Sprintf("%s?%s", sWithoutQuery, queryValsStr)
			} else {
				s = sWithoutQuery
			}
		}
	}

	v.uri = s

	return nil
}

func (v *secretValue) String() string {
	return v.uri
}

func (v *secretValue) Get(ctx context.Context, c *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	var opts []dagger.SecretOpts
	if v.cacheKey != "" {
		opts = append(opts, dagger.SecretOpts{
			CacheKey: v.cacheKey,
		})
	}
	return c.Secret(v.uri, opts...), nil
}

// serviceValue is a pflag.Value that builds a dagger.Service from a host:port
// combination.
type serviceValue struct {
	address string // for string representation
	host    string
	ports   []dagger.PortForward
}

func (v *serviceValue) Type() string {
	return Service
}

func (v *serviceValue) String() string {
	return v.address
}

func (v *serviceValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("service address cannot be empty")
	}
	u, err := url.Parse(s)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "tcp":
		host, port, err := net.SplitHostPort(u.Host)
		if err != nil {
			return err
		}
		nPort, err := strconv.Atoi(port)
		if err != nil {
			return err
		}
		v.host = host
		v.ports = append(v.ports, dagger.PortForward{
			Backend:  nPort,
			Frontend: nPort,
			Protocol: dagger.NetworkProtocolTcp,
		})
	case "udp":
		host, port, err := net.SplitHostPort(u.Host)
		if err != nil {
			return err
		}
		nPort, err := strconv.Atoi(port)
		if err != nil {
			return err
		}
		v.host = host
		v.ports = append(v.ports, dagger.PortForward{
			Backend:  nPort,
			Frontend: nPort,
			Protocol: dagger.NetworkProtocolUdp,
		})
	default:
		return fmt.Errorf("unsupported service address. Must be a valid tcp:// or udp:// URL")
	}
	v.address = s
	return nil
}

func (v *serviceValue) Get(ctx context.Context, c *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	svc, err := c.Host().Service(v.ports, dagger.HostServiceOpts{Host: v.host}).Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start service: %w", err)
	}
	return svc, nil
}

// portForwardValue is a pflag.Value that builds a dagger.
type portForwardValue struct {
	frontend int
	backend  int
}

func (v *portForwardValue) Type() string {
	return PortForward
}

func (v *portForwardValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("portForward setting cannot be empty")
	}

	frontendStr, backendStr, ok := strings.Cut(s, ":")
	if !ok {
		return fmt.Errorf("portForward setting not in the form of frontend:backend: %q", s)
	}

	frontend, err := strconv.Atoi(frontendStr)
	if err != nil {
		return fmt.Errorf("portForward frontend not a valid integer: %q", frontendStr)
	}
	v.frontend = frontend

	backend, err := strconv.Atoi(backendStr)
	if err != nil {
		return fmt.Errorf("portForward backend not a valid integer: %q", backendStr)
	}
	v.backend = backend

	return nil
}

func (v *portForwardValue) String() string {
	return fmt.Sprintf("%d:%d", v.frontend, v.backend)
}

func (v *portForwardValue) Get(_ context.Context, c *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	return &dagger.PortForward{
		Frontend: v.frontend,
		Backend:  v.backend,
	}, nil
}

type socketValue struct {
	path string
}

func (v *socketValue) Type() string {
	return Socket
}

func (v *socketValue) String() string {
	return v.path
}

func (v *socketValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("socket path cannot be empty")
	}
	s = strings.TrimPrefix(s, "unix://") // allow unix:// scheme
	v.path = s
	return nil
}

func (v *socketValue) Get(ctx context.Context, c *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	return c.Host().UnixSocket(v.path), nil
}

// cacheVolumeValue is a pflag.Value that builds a dagger.CacheVolume from a
// volume name.
type cacheVolumeValue struct {
	name string
}

func (v *cacheVolumeValue) Type() string {
	return CacheVolume
}

func (v *cacheVolumeValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("cacheVolume name cannot be empty")
	}
	v.name = s
	return nil
}

func (v *cacheVolumeValue) String() string {
	return v.name
}

func (v *cacheVolumeValue) Get(_ context.Context, dag *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	if v.String() == "" {
		return nil, fmt.Errorf("cacheVolume name cannot be empty")
	}
	return dag.CacheVolume(v.name), nil
}

type moduleValue struct {
	ref string
}

func (v *moduleValue) Type() string {
	return Module
}

func (v *moduleValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("module ref cannot be empty")
	}
	v.ref = s
	return nil
}

func (v *moduleValue) String() string {
	return v.ref
}

func (v *moduleValue) Get(ctx context.Context, dag *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	if v.ref == "" {
		return nil, fmt.Errorf("module ref cannot be empty")
	}
	return dag.ModuleSource(v.ref).AsModule().Sync(ctx)
}

type moduleSourceValue struct {
	ref string
}

func (v *moduleSourceValue) Type() string {
	return ModuleSource
}

func (v *moduleSourceValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("module source ref cannot be empty")
	}
	v.ref = s
	return nil
}

func (v *moduleSourceValue) String() string {
	return v.ref
}

func (v *moduleSourceValue) Get(ctx context.Context, dag *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	if v.ref == "" {
		return nil, fmt.Errorf("module source ref cannot be empty")
	}
	return dag.ModuleSource(v.ref).Sync(ctx)
}

type platformValue struct {
	platform string
}

func (v *platformValue) Type() string {
	return Platform
}

func (v *platformValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("platform cannot be empty")
	}
	if s == "current" {
		s = platforms.DefaultString()
	}
	v.platform = s
	return nil
}

func (v *platformValue) String() string {
	return v.platform
}

func (v *platformValue) Get(ctx context.Context, dag *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	if v.platform == "" {
		return nil, fmt.Errorf("platform cannot be empty")
	}
	return v.platform, nil
}

type buildArgValue struct {
	name  string
	value string
}

func (v *buildArgValue) Type() string {
	return BuildArg
}

func (v *buildArgValue) Set(s string) error {
	if !strings.Contains(s, "=") {
		return fmt.Errorf("%s must be formatted as name=value", s)
	}
	pair := strings.Trim(s, `"`)
	name, value, found := strings.Cut(pair, "=")
	if !found {
		return fmt.Errorf("%s must be formatted as name=value", pair)
	}
	if name == "" {
		return fmt.Errorf("%s cannot have an empty name", pair)
	}
	v.name = name
	v.value = value
	return nil
}

func (v *buildArgValue) String() string {
	return fmt.Sprintf("%s=%s", v.name, v.value)
}

func (v *buildArgValue) Get(ctx context.Context, dag *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	if v.name == "" {
		return nil, fmt.Errorf("build arg cannot be empty")
	}
	return dagger.BuildArg{Name: v.name, Value: v.value}, nil
}

type gitRepositoryValue struct {
	address string
}

func (v *gitRepositoryValue) Type() string {
	return GitRepository
}

func (v *gitRepositoryValue) String() string {
	return v.address
}

func (v *gitRepositoryValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("git repository address cannot be empty")
	}
	v.address = s
	return nil
}

func (v *gitRepositoryValue) Get(ctx context.Context, dag *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	if v.String() == "" {
		return nil, fmt.Errorf("git repository address cannot be empty")
	}

	// Try parsing as a Git URL
	gitURL, err := parseGitURL(v.String())
	if err == nil {
		if gitURL.Fragment.Ref != "" {
			return nil, fmt.Errorf("git repository cannot contain ref")
		}
		if gitURL.Fragment.Subdir != "" {
			return nil, fmt.Errorf("git repository cannot contain subdir")
		}
		return dag.Git(gitURL.Remote()), nil
	}

	// Otherwise it's a local dir path
	path := v.String()
	path, err = getLocalPath(path)
	if err != nil {
		return nil, err
	}

	return dag.Host().Directory(path).AsGit(), nil
}

type gitRefValue struct {
	address string
}

func (v *gitRefValue) Type() string {
	return GitRef
}

func (v *gitRefValue) String() string {
	return v.address
}

func (v *gitRefValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("git ref address cannot be empty")
	}
	v.address = s
	return nil
}

func (v *gitRefValue) Get(ctx context.Context, dag *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	if v.String() == "" {
		return nil, fmt.Errorf("git ref address cannot be empty")
	}

	// Try parsing as a Git URL
	gitURL, err := parseGitURL(v.String())
	if err == nil {
		if gitURL.Fragment.Subdir != "" {
			return nil, fmt.Errorf("git repository cannot contain subdir")
		}
		repo := dag.Git(gitURL.Remote())
		if ref := gitURL.Fragment.Ref; ref != "" {
			return repo.Ref(ref), nil
		}
		return repo.Head(), nil
	}

	// Otherwise it's a local dir path
	path, ref, _ := strings.Cut(v.String(), "#")
	path, err = getLocalPath(path)
	if err != nil {
		return nil, err
	}

	repo := dag.Host().Directory(path).AsGit()
	if ref != "" {
		return repo.Ref(ref), nil
	}
	return repo.Head(), nil
}

// AddFlag adds a flag appropriate for the argument type. Should return a
// pointer to the value.
//
//nolint:gocyclo
func (r *modFunctionArg) AddFlag(flags *pflag.FlagSet) error {
	name := r.FlagName()
	usage := r.Description

	if flags.Lookup(name) != nil {
		return fmt.Errorf("flag already exists: %s", name)
	}

	switch r.TypeDef.Kind {
	case dagger.TypeDefKindStringKind:
		val, _ := getDefaultValue[string](r)
		flags.String(name, val, usage)
		return nil

	case dagger.TypeDefKindIntegerKind:
		val, _ := getDefaultValue[int](r)
		flags.Int(name, val, usage)
		return nil

	case dagger.TypeDefKindFloatKind:
		val, _ := getDefaultValue[float64](r)
		flags.Float64(name, val, usage)
		return nil

	case dagger.TypeDefKindBooleanKind:
		val, _ := getDefaultValue[bool](r)
		flags.Bool(name, val, usage)
		return nil

	case dagger.TypeDefKindScalarKind:
		scalarName := r.TypeDef.AsScalar.Name
		defVal, _ := getDefaultValue[string](r)

		if val := GetCustomFlagValue(scalarName); val != nil {
			if defVal != "" {
				val.Set(defVal)
			}
			flags.Var(val, name, usage)
			return nil
		}

		flags.String(name, defVal, usage)
		return nil

	case dagger.TypeDefKindEnumKind:
		enumName := r.TypeDef.AsEnum.Name
		defVal, _ := getDefaultValue[string](r)

		if val := GetCustomFlagValue(enumName); val != nil {
			if defVal != "" {
				val.Set(defVal)
			}
			flags.Var(val, name, usage)
			return nil
		}

		val := newEnumValue(r.TypeDef.AsEnum, defVal)
		flags.Var(val, name, usage)

		return nil

	case dagger.TypeDefKindObjectKind:
		objName := r.TypeDef.AsObject.Name

		if name == "id" && r.TypeDef.AsObject.IsCore() {
			// FIXME: The core TypeDefs have ids converted to objects, but we'd
			// need the CLI to recognize that and either use the object's ID
			// or allow inputing it directly. Just don't support it for now.
			return &UnsupportedFlagError{
				Name: name,
				Type: fmt.Sprintf("%sID", objName),
			}
		}

		if val := GetCustomFlagValue(objName); val != nil {
			flags.Var(val, name, usage)
			return nil
		}

		// TODO: default to JSON?
		return &UnsupportedFlagError{
			Name: name,
			Type: fmt.Sprintf("%q object", objName),
		}

	case dagger.TypeDefKindInputKind:
		inputName := r.TypeDef.AsInput.Name

		if val := GetCustomFlagValue(inputName); val != nil {
			flags.Var(val, name, usage)
			return nil
		}

		// TODO: default to JSON?
		return &UnsupportedFlagError{
			Name: name,
			Type: fmt.Sprintf("%q input", inputName),
		}

	case dagger.TypeDefKindListKind:
		elementType := r.TypeDef.AsList.ElementTypeDef

		switch elementType.Kind {
		case dagger.TypeDefKindStringKind:
			val, _ := getDefaultValue[[]string](r)
			flags.StringSlice(name, val, usage)
			return nil

		case dagger.TypeDefKindIntegerKind:
			val, _ := getDefaultValue[[]int](r)
			flags.IntSlice(name, val, usage)
			return nil

		case dagger.TypeDefKindFloatKind:
			val, _ := getDefaultValue[[]float64](r)
			flags.Float64Slice(name, val, usage)
			return nil

		case dagger.TypeDefKindBooleanKind:
			val, _ := getDefaultValue[[]bool](r)
			flags.BoolSlice(name, val, usage)
			return nil

		case dagger.TypeDefKindScalarKind:
			scalarName := elementType.AsScalar.Name
			defVal, _ := getDefaultValue[[]string](r)

			val, err := GetCustomFlagValueSlice(scalarName, defVal)
			if err != nil {
				return err
			}
			if val != nil {
				flags.Var(val, name, usage)
				return nil
			}

			flags.StringSlice(name, defVal, usage)
			return nil

		case dagger.TypeDefKindEnumKind:
			enumName := elementType.AsEnum.Name
			defVal, _ := getDefaultValue[[]string](r)

			val, err := GetCustomFlagValueSlice(enumName, defVal)
			if err != nil {
				return err
			}
			if val != nil {
				flags.Var(val, name, usage)
				return nil
			}

			val = newEnumSliceValue(elementType.AsEnum, defVal)
			flags.Var(val, name, usage)

			return nil

		case dagger.TypeDefKindObjectKind:
			objName := elementType.AsObject.Name

			val, err := GetCustomFlagValueSlice(objName, nil)
			if err != nil {
				return err
			}
			if val != nil {
				flags.Var(val, name, usage)
				return nil
			}

			// TODO: default to JSON?
			return &UnsupportedFlagError{
				Name: name,
				Type: fmt.Sprintf("list of %q objects", objName),
			}

		case dagger.TypeDefKindInputKind:
			inputName := elementType.AsInput.Name

			val, err := GetCustomFlagValueSlice(inputName, nil)
			if err != nil {
				return err
			}
			if val != nil {
				flags.Var(val, name, usage)
				return nil
			}

			// TODO: default to JSON?
			return &UnsupportedFlagError{
				Name: name,
				Type: fmt.Sprintf("list of %q inputs", inputName),
			}

		case dagger.TypeDefKindListKind:
			return &UnsupportedFlagError{
				Name: name,
				Type: "list of lists",
			}
		}
	}

	return &UnsupportedFlagError{Name: name}
}

func (r *modFunctionArg) GetFlag(flags *pflag.FlagSet) (*pflag.Flag, error) {
	flag := flags.Lookup(r.FlagName())
	if flag == nil {
		return nil, fmt.Errorf("no flag for %q", r.FlagName())
	}
	return flag, nil
}

func (r *modFunctionArg) GetFlagValue(ctx context.Context, flag *pflag.Flag, dag *dagger.Client, md *moduleDef) (any, error) {
	v := flag.Value

	switch val := v.(type) {
	case DaggerValue:
		obj, err := val.Get(ctx, dag, md.Source, r)
		if err != nil {
			return nil, fmt.Errorf("failed to get value for argument %q: %w", r.FlagName(), err)
		}
		if obj == nil {
			return nil, fmt.Errorf("no value for argument: %s", r.FlagName())
		}
		return obj, nil
	case pflag.SliceValue:
		return val.GetSlice(), nil
	default:
		return v, nil
	}
}

func readAsCSV(val string) ([]string, error) {
	if val == "" {
		return []string{}, nil
	}
	stringReader := strings.NewReader(val)
	csvReader := csv.NewReader(stringReader)
	return csvReader.Read()
}

func writeAsCSV(vals []string) (string, error) {
	b := &bytes.Buffer{}
	w := csv.NewWriter(b)
	err := w.Write(vals)
	if err != nil {
		return "", err
	}
	w.Flush()
	return strings.TrimSuffix(b.String(), "\n"), nil
}

func getLocalPath(path string) (string, error) {
	// allow `file://` scheme or no scheme
	path = strings.TrimPrefix(path, "file://")

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path, err = pathutil.ExpandHomeDir(homeDir, path)
	if err != nil {
		return "", fmt.Errorf("failed to expand home directory: %w", err)
	}
	if !filepath.IsAbs(path) {
		path, err = pathutil.Abs(path)
		if err != nil {
			return "", fmt.Errorf("failed to resolve absolute path: %w", err)
		}
	}

	// make windows paths usable in the Linux engine container
	path = filepath.ToSlash(path)

	return path, nil
}
