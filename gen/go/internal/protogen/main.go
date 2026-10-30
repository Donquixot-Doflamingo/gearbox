// SPDX-License-Identifier: MIT

// Command protogen generates the Go code in gen/go/gearbox/v1 from
// proto/gearbox/v1/*.proto without protoc or buf.
//
// It parses the proto files with github.com/bufbuild/protocompile, builds a
// pluginpb.CodeGeneratorRequest exactly like protoc would, and feeds it to the
// standard protoc-gen-go and protoc-gen-go-grpc plugins (built from the module
// graph of gen/go, so their versions are pinned in gen/go/go.mod). The output
// is byte-for-byte what `buf generate` with gen/go/buf.gen.yaml produces for
// the same plugin versions.
//
// Usage (from the repository root):
//
//	go run ./gen/go/internal/protogen -proto proto -out gen/go
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

type plugin struct {
	name  string // binary name
	pkg   string // go package to build
	param string
}

var plugins = []plugin{
	{name: "protoc-gen-go", pkg: "google.golang.org/protobuf/cmd/protoc-gen-go", param: "paths=import,module=github.com/ORG/gearbox/gen/go"},
	{name: "protoc-gen-go-grpc", pkg: "google.golang.org/grpc/cmd/protoc-gen-go-grpc", param: "paths=import,module=github.com/ORG/gearbox/gen/go"},
}

func main() {
	protoDir := flag.String("proto", "proto", "proto source root")
	outDir := flag.String("out", "gen/go", "output root (module root of gen/go)")
	modDir := flag.String("moddir", "gen/go", "directory of the gen/go module (plugins are built from its module graph)")
	flag.Parse()
	if err := run(context.Background(), *protoDir, *outDir, *modDir); err != nil {
		fmt.Fprintln(os.Stderr, "protogen:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, protoDir, outDir, modDir string) error {
	files, err := findProtos(protoDir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no .proto files under %s", protoDir)
	}
	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			ImportPaths: []string{protoDir},
		}),
		SourceInfoMode: protocompile.SourceInfoStandard,
	}
	compiled, err := compiler.Compile(ctx, files...)
	if err != nil {
		return fmt.Errorf("compile protos: %w", err)
	}

	// Collect every file (including imports) in dependency order.
	var all []*descriptorpb.FileDescriptorProto
	seen := map[string]bool{}
	var add func(fd protoreflect.FileDescriptor)
	add = func(fd protoreflect.FileDescriptor) {
		if seen[fd.Path()] {
			return
		}
		imports := fd.Imports()
		for i := 0; i < imports.Len(); i++ {
			add(imports.Get(i).FileDescriptor)
		}
		seen[fd.Path()] = true
		all = append(all, protodesc.ToFileDescriptorProto(fd))
	}
	for _, f := range compiled {
		add(f)
	}

	binDir, err := os.MkdirTemp("", "protogen-bin")
	if err != nil {
		return err
	}
	defer os.RemoveAll(binDir)

	for _, p := range plugins {
		bin := filepath.Join(binDir, p.name)
		build := exec.CommandContext(ctx, "go", "build", "-o", bin, p.pkg)
		build.Dir = modDir
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			return fmt.Errorf("build %s: %w", p.name, err)
		}
		req := &pluginpb.CodeGeneratorRequest{
			FileToGenerate:  files,
			Parameter:       proto.String(p.param),
			ProtoFile:       all,
			CompilerVersion: &pluginpb.Version{Major: proto.Int32(5), Minor: proto.Int32(29), Patch: proto.Int32(0)},
		}
		resp, err := invoke(ctx, bin, req)
		if err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
		for _, f := range resp.GetFile() {
			path := filepath.Join(outDir, filepath.FromSlash(f.GetName()))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(f.GetContent()), 0o644); err != nil {
				return err
			}
			fmt.Println("wrote", path)
		}
	}
	return nil
}

func invoke(ctx context.Context, bin string, req *pluginpb.CodeGeneratorRequest) (*pluginpb.CodeGeneratorResponse, error) {
	in, err := proto.Marshal(req)
	if err != nil {
		return nil, err
	}
	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin)
	cmd.Stdin = bytes.NewReader(in)
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, stderr.String())
	}
	resp := &pluginpb.CodeGeneratorResponse{}
	if err := proto.Unmarshal(out.Bytes(), resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, errors.New(resp.GetError())
	}
	return resp, nil
}

func findProtos(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".proto") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(files)
	return files, err
}
