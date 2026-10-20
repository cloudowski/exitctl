// Package fixture generates synthetic, deterministic repositories for
// performance testing (T097, SC-001). It is dev-only: the exitctl binary never
// imports it.
//
// The generated repository has:
//
//   - infra/: a Terraform root module that calls local modules under
//     infra/modules/mod-NNN and wires some module outputs into other modules.
//   - each module: a mix of catalog-known AWS resources (VPC, subnets,
//     security groups, IAM roles, DynamoDB tables, SQS queues, Lambda
//     functions, S3 buckets) with realistic cross references, plus some types
//     the catalog does not know.
//   - services/svc-NNN/: application components with a manifest (package.json,
//     requirements.txt or go.mod) and source files, some of which import AWS
//     SDKs.
//
// The same Options always produce byte-identical output.
package fixture

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
)

// Options controls the size and shape of the generated repository.
type Options struct {
	// Resources is the total number of Terraform resources.
	Resources int
	// Modules is the number of local modules the resources are spread
	// across (0: one module per 50 resources).
	Modules int
	// SourceFiles is the total number of source code files.
	SourceFiles int
	// FilesPerComponent is the number of source files per component
	// (0: 100).
	FilesPerComponent int
	// SDKShare is the fraction (0..1) of source files that import an AWS
	// SDK, within components that depend on one (default 0.2 when 0).
	SDKShare float64
	// Seed makes the output deterministic.
	Seed uint64
}

// Stats summarizes what was generated.
type Stats struct {
	Resources   int
	Modules     int
	Components  int
	SourceFiles int
	Manifests   int
	Bytes       int64
}

func (o *Options) defaults() {
	if o.Modules <= 0 {
		o.Modules = (o.Resources + 49) / 50
		if o.Modules == 0 {
			o.Modules = 1
		}
	}
	if o.FilesPerComponent <= 0 {
		o.FilesPerComponent = 100
	}
	if o.SDKShare <= 0 {
		o.SDKShare = 0.2
	}
}

type gen struct {
	dir   string
	rng   *rand.Rand
	stats Stats
}

// Generate writes the synthetic repository into dir (created if missing).
func Generate(dir string, o Options) (Stats, error) {
	o.defaults()
	g := &gen{dir: dir, rng: rand.New(rand.NewPCG(o.Seed, o.Seed^0x9e3779b97f4a7c15))}
	if err := g.terraform(o); err != nil {
		return g.stats, err
	}
	if err := g.services(o); err != nil {
		return g.stats, err
	}
	return g.stats, nil
}

func (g *gen) write(rel string, data []byte) error {
	p := filepath.Join(g.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	g.stats.Bytes += int64(len(data))
	return os.WriteFile(p, data, 0o644)
}

// ---------------------------------------------------------------- Terraform

// module is the state of one module while its resources are generated.
type module struct {
	b        bytes.Buffer
	files    [][]byte
	inFile   int
	vpc      string
	subnets  []string
	sgs      []string
	roles    []string
	tables   []string
	queues   []string
	buckets  []string
	lambdas  []string
	n        int
	hasInput bool
}

const resourcesPerFile = 25

func (m *module) add(format string, args ...any) {
	fmt.Fprintf(&m.b, format, args...)
	m.b.WriteString("\n")
	m.n++
	m.inFile++
	if m.inFile >= resourcesPerFile {
		m.flush()
	}
}

func (m *module) flush() {
	if m.b.Len() > 0 {
		m.files = append(m.files, append([]byte(nil), m.b.Bytes()...))
		m.b.Reset()
	}
	m.inFile = 0
}

func (g *gen) pick(xs []string) string { return xs[g.rng.IntN(len(xs))] }

// unknownTypes are resource types the catalog does not map.
var unknownTypes = []string{"acme_widget", "example_thing", "internal_registry_entry", "random_id", "null_resource", "time_sleep"}

func (g *gen) terraform(o Options) error {
	if o.Resources <= 0 {
		return nil
	}
	var root bytes.Buffer
	root.WriteString(`terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.60"
    }
  }
}

provider "aws" {
  region = "eu-west-1"
}
`)
	per := o.Resources / o.Modules
	extra := o.Resources % o.Modules
	for i := 0; i < o.Modules; i++ {
		n := per
		if i < extra {
			n++
		}
		if n == 0 {
			continue
		}
		name := fmt.Sprintf("mod-%03d", i)
		m := g.module(i, n)
		m.flush()
		for j, f := range m.files {
			if err := g.write(fmt.Sprintf("infra/modules/%s/main-%02d.tf", name, j), f); err != nil {
				return err
			}
		}
		if err := g.write("infra/modules/"+name+"/io.tf", moduleIO(m)); err != nil {
			return err
		}
		fmt.Fprintf(&root, "\nmodule %q {\n  source = \"./modules/%s\"\n  name   = %q\n", name, name, name)
		// Wire the previous module's queue into this one: a cross-module
		// data-flow reference through outputs and variables.
		if i > 0 && m.hasInput {
			fmt.Fprintf(&root, "  upstream_queue_arn = module.mod-%03d.queue_arn\n", i-1)
		}
		root.WriteString("}\n")
		g.stats.Resources += m.n
		g.stats.Modules++
	}
	return g.write("infra/main.tf", root.Bytes())
}

func moduleIO(m *module) []byte {
	var b bytes.Buffer
	b.WriteString("variable \"name\" {\n  type = string\n}\n\n")
	b.WriteString("variable \"upstream_queue_arn\" {\n  type    = string\n  default = \"\"\n}\n\n")
	if len(m.queues) > 0 {
		fmt.Fprintf(&b, "output \"queue_arn\" {\n  value = aws_sqs_queue.%s.arn\n}\n", m.queues[0])
	} else {
		b.WriteString("output \"queue_arn\" {\n  value = \"\"\n}\n")
	}
	return b.Bytes()
}

// module generates n resources for module i.
func (g *gen) module(i, n int) *module {
	m := &module{}
	emitBase := func() bool { return m.n < n }
	if emitBase() {
		m.vpc = "main"
		m.add("resource \"aws_vpc\" \"main\" {\n  cidr_block           = \"10.%d.0.0/16\"\n  enable_dns_hostnames = true\n\n  tags = {\n    Name = var.name\n  }\n}", i%250)
	}
	for k := 0; k < 2 && emitBase(); k++ {
		g.subnet(m, i)
	}
	if emitBase() {
		g.securityGroup(m)
	}
	if emitBase() {
		g.role(m)
	}
	for m.n < n {
		r := g.rng.IntN(100)
		switch {
		case r < 18:
			g.table(m)
		case r < 34:
			g.queue(m)
		case r < 50 && len(m.roles) > 0:
			g.lambda(m)
		case r < 62:
			g.bucket(m)
		case r < 70:
			g.role(m)
		case r < 76 && m.vpc != "":
			g.securityGroup(m)
		case r < 82 && m.vpc != "":
			g.subnet(m, i)
		case r < 88 && len(m.lambdas) > 0 && (len(m.queues) > 0 || len(m.tables) > 0):
			g.mapping(m)
		default:
			g.unknown(m)
		}
	}
	return m
}

func (g *gen) subnet(m *module, i int) {
	name := fmt.Sprintf("s%d", len(m.subnets))
	m.subnets = append(m.subnets, name)
	m.add("resource \"aws_subnet\" %q {\n  vpc_id            = aws_vpc.%s.id\n  cidr_block        = \"10.%d.%d.0/24\"\n  availability_zone = \"eu-west-1%c\"\n}",
		name, m.vpc, i%250, len(m.subnets)%250, 'a'+rune(len(m.subnets)%3))
}

func (g *gen) securityGroup(m *module) {
	name := fmt.Sprintf("sg%d", len(m.sgs))
	m.sgs = append(m.sgs, name)
	m.add("resource \"aws_security_group\" %q {\n  name   = \"${var.name}-%s\"\n  vpc_id = aws_vpc.%s.id\n\n  egress {\n    from_port   = 0\n    to_port     = 0\n    protocol    = \"-1\"\n    cidr_blocks = [\"0.0.0.0/0\"]\n  }\n}",
		name, name, m.vpc)
}

func (g *gen) role(m *module) {
	name := fmt.Sprintf("role%d", len(m.roles))
	m.roles = append(m.roles, name)
	m.add("resource \"aws_iam_role\" %q {\n  name = \"${var.name}-%s\"\n\n  assume_role_policy = jsonencode({\n    Version = \"2012-10-17\"\n    Statement = [{\n      Effect    = \"Allow\"\n      Action    = \"sts:AssumeRole\"\n      Principal = { Service = \"lambda.amazonaws.com\" }\n    }]\n  })\n}",
		name, name)
}

func (g *gen) table(m *module) {
	name := fmt.Sprintf("t%d", len(m.tables))
	m.tables = append(m.tables, name)
	stream := ""
	if g.rng.IntN(3) == 0 {
		stream = "\n\n  stream_enabled   = true\n  stream_view_type = \"NEW_IMAGE\""
	}
	m.add("resource \"aws_dynamodb_table\" %q {\n  name         = \"${var.name}-%s\"\n  billing_mode = \"PAY_PER_REQUEST\"\n  hash_key     = \"id\"\n\n  attribute {\n    name = \"id\"\n    type = \"S\"\n  }%s\n}",
		name, name, stream)
}

func (g *gen) queue(m *module) {
	name := fmt.Sprintf("q%d", len(m.queues))
	m.queues = append(m.queues, name)
	dlq := ""
	if len(m.queues) > 1 && g.rng.IntN(2) == 0 {
		dlq = fmt.Sprintf("\n  redrive_policy = jsonencode({\n    deadLetterTargetArn = aws_sqs_queue.%s.arn\n    maxReceiveCount     = 5\n  })", m.queues[0])
	}
	m.add("resource \"aws_sqs_queue\" %q {\n  name                       = \"${var.name}-%s\"\n  visibility_timeout_seconds = 60%s\n}", name, name, dlq)
}

func (g *gen) bucket(m *module) {
	name := fmt.Sprintf("b%d", len(m.buckets))
	m.buckets = append(m.buckets, name)
	m.add("resource \"aws_s3_bucket\" %q {\n  bucket = \"${var.name}-%s\"\n\n  tags = {\n    Owner = \"platform\"\n  }\n}", name, name)
}

func (g *gen) lambda(m *module) {
	name := fmt.Sprintf("fn%d", len(m.lambdas))
	m.lambdas = append(m.lambdas, name)
	var b strings.Builder
	fmt.Fprintf(&b, "resource \"aws_lambda_function\" %q {\n  function_name = \"${var.name}-%s\"\n  role          = aws_iam_role.%s.arn\n  runtime       = \"nodejs20.x\"\n  handler       = \"index.handler\"\n  filename      = \"build/%s.zip\"\n",
		name, name, g.pick(m.roles), name)
	if len(m.subnets) > 0 && len(m.sgs) > 0 {
		fmt.Fprintf(&b, "\n  vpc_config {\n    subnet_ids         = [aws_subnet.%s.id]\n    security_group_ids = [aws_security_group.%s.id]\n  }\n", g.pick(m.subnets), g.pick(m.sgs))
	}
	var env []string
	if len(m.tables) > 0 {
		env = append(env, fmt.Sprintf("      TABLE_NAME = aws_dynamodb_table.%s.name", g.pick(m.tables)))
	}
	if len(m.queues) > 0 {
		env = append(env, fmt.Sprintf("      QUEUE_URL  = aws_sqs_queue.%s.url", g.pick(m.queues)))
	}
	if len(m.buckets) > 0 {
		env = append(env, fmt.Sprintf("      BUCKET     = aws_s3_bucket.%s.bucket", g.pick(m.buckets)))
	}
	if !m.hasInput {
		env = append(env, "      UPSTREAM   = var.upstream_queue_arn")
		m.hasInput = true
	}
	if len(env) > 0 {
		fmt.Fprintf(&b, "\n  environment {\n    variables = {\n%s\n    }\n  }\n", strings.Join(env, "\n"))
	}
	b.WriteString("}")
	m.add("%s", b.String())
}

func (g *gen) mapping(m *module) {
	name := fmt.Sprintf("map%d", m.n)
	fn := g.pick(m.lambdas)
	if len(m.queues) > 0 && (len(m.tables) == 0 || g.rng.IntN(2) == 0) {
		m.add("resource \"aws_lambda_event_source_mapping\" %q {\n  event_source_arn = aws_sqs_queue.%s.arn\n  function_name    = aws_lambda_function.%s.arn\n  batch_size       = 10\n}",
			name, g.pick(m.queues), fn)
		return
	}
	m.add("resource \"aws_lambda_event_source_mapping\" %q {\n  event_source_arn  = aws_dynamodb_table.%s.stream_arn\n  function_name     = aws_lambda_function.%s.arn\n  starting_position = \"LATEST\"\n}",
		name, g.pick(m.tables), fn)
}

func (g *gen) unknown(m *module) {
	typ := g.pick(unknownTypes)
	name := fmt.Sprintf("u%d", m.n)
	ref := ""
	if len(m.buckets) > 0 && g.rng.IntN(2) == 0 {
		ref = fmt.Sprintf("\n  target = aws_s3_bucket.%s.arn", g.pick(m.buckets))
	}
	m.add("resource %q %q {\n  label = \"${var.name}-%s\"%s\n}", typ, name, name, ref)
}

// ---------------------------------------------------------------- Sources

type ecosystem int

const (
	ecoNPM ecosystem = iota
	ecoPy
	ecoGo
)

// sdk describes one AWS service SDK per ecosystem.
type sdk struct {
	npmPkg, npmImport string
	goPkg             string
	pyService         string
}

var sdks = []sdk{
	{"@aws-sdk/client-dynamodb", "DynamoDBClient", "github.com/aws/aws-sdk-go-v2/service/dynamodb", "dynamodb"},
	{"@aws-sdk/client-sqs", "SQSClient", "github.com/aws/aws-sdk-go-v2/service/sqs", "sqs"},
	{"@aws-sdk/client-s3", "S3Client", "github.com/aws/aws-sdk-go-v2/service/s3", "s3"},
	{"@aws-sdk/client-lambda", "LambdaClient", "github.com/aws/aws-sdk-go-v2/service/lambda", "lambda"},
}

func (g *gen) services(o Options) error {
	if o.SourceFiles <= 0 {
		return nil
	}
	comps := (o.SourceFiles + o.FilesPerComponent - 1) / o.FilesPerComponent
	left := o.SourceFiles
	for c := 0; c < comps; c++ {
		n := min(o.FilesPerComponent, left)
		left -= n
		eco := ecosystem(c % 3)
		usesSDK := c%4 != 3 // three out of four components depend on an SDK
		var used []sdk
		if usesSDK {
			k := 1 + g.rng.IntN(len(sdks))
			perm := g.rng.Perm(len(sdks))
			for _, p := range perm[:k] {
				used = append(used, sdks[p])
			}
		}
		dir := fmt.Sprintf("services/svc-%03d", c)
		if err := g.write(dir+"/"+manifestName(eco), manifest(eco, c, used)); err != nil {
			return err
		}
		g.stats.Manifests++
		for f := 0; f < n; f++ {
			var s *sdk
			if len(used) > 0 && g.rng.Float64() < o.SDKShare {
				s = &used[g.rng.IntN(len(used))]
			}
			rel := fmt.Sprintf("%s/%s/pkg%02d/file%04d%s", dir, srcDir(eco), f/20, f, ext(eco))
			if err := g.write(rel, g.source(eco, f, s)); err != nil {
				return err
			}
			g.stats.SourceFiles++
		}
		g.stats.Components++
	}
	return nil
}

func manifestName(e ecosystem) string {
	switch e {
	case ecoPy:
		return "requirements.txt"
	case ecoGo:
		return "go.mod"
	}
	return "package.json"
}

func srcDir(e ecosystem) string {
	if e == ecoPy {
		return "app"
	}
	if e == ecoGo {
		return "internal"
	}
	return "src"
}

func ext(e ecosystem) string {
	switch e {
	case ecoPy:
		return ".py"
	case ecoGo:
		return ".go"
	}
	return ".ts"
}

func manifest(e ecosystem, c int, used []sdk) []byte {
	var b bytes.Buffer
	switch e {
	case ecoNPM:
		deps := []string{`    "express": "^4.19.2"`, `    "zod": "^3.23.8"`}
		for _, s := range used {
			deps = append(deps, fmt.Sprintf("    %q: \"^3.600.0\"", s.npmPkg))
		}
		fmt.Fprintf(&b, "{\n  \"name\": \"svc-%03d\",\n  \"version\": \"1.0.0\",\n  \"private\": true,\n  \"dependencies\": {\n%s\n  }\n}\n", c, strings.Join(deps, ",\n"))
	case ecoPy:
		b.WriteString("fastapi==0.111.0\npydantic>=2.7\n")
		if len(used) > 0 {
			b.WriteString("boto3==1.34.120\n")
		}
	case ecoGo:
		fmt.Fprintf(&b, "module example.com/svc-%03d\n\ngo 1.22\n\nrequire (\n\tgithub.com/google/uuid v1.6.0\n", c)
		if len(used) > 0 {
			b.WriteString("\tgithub.com/aws/aws-sdk-go-v2 v1.30.0\n")
		}
		for _, s := range used {
			fmt.Fprintf(&b, "\t%s v1.30.0\n", s.goPkg)
		}
		b.WriteString(")\n")
	}
	return b.Bytes()
}

// source returns one source file of roughly 40-80 lines. When s is not nil
// the file imports and uses that SDK.
func (g *gen) source(e ecosystem, f int, s *sdk) []byte {
	var b bytes.Buffer
	body := 10 + g.rng.IntN(10)
	switch e {
	case ecoNPM:
		b.WriteString("import express from \"express\";\nimport { z } from \"zod\";\n")
		if s != nil {
			fmt.Fprintf(&b, "import { %s } from %q;\n\nconst client = new %s({});\n", s.npmImport, s.npmPkg, s.npmImport)
		}
		for i := 0; i < body; i++ {
			fmt.Fprintf(&b, "\nexport function handler%d_%d(input: unknown): string {\n  const v = z.string().parse(input);\n  return `${v}-%d`;\n}\n", f, i, i)
		}
	case ecoPy:
		b.WriteString("from pydantic import BaseModel\n")
		if s != nil {
			fmt.Fprintf(&b, "import boto3\n\nclient = boto3.client(%q)\n", s.pyService)
		}
		for i := 0; i < body; i++ {
			fmt.Fprintf(&b, "\n\nclass Model%d_%d(BaseModel):\n    id: str\n    value: int = %d\n\n    def describe(self) -> str:\n        return f\"{self.id}:{self.value}\"\n", f, i, i)
		}
	case ecoGo:
		fmt.Fprintf(&b, "package pkg%02d\n\nimport (\n\t\"fmt\"\n", f/20)
		if s != nil {
			fmt.Fprintf(&b, "\n\t%q\n", s.goPkg)
		}
		b.WriteString(")\n")
		if s != nil {
			short := s.goPkg[strings.LastIndex(s.goPkg, "/")+1:]
			fmt.Fprintf(&b, "\nvar _ = %s.Options{}\n", short)
		}
		for i := 0; i < body; i++ {
			fmt.Fprintf(&b, "\n// Describe%d_%d formats a value.\nfunc Describe%d_%d(v int) string {\n\treturn fmt.Sprintf(\"%%d-%d\", v)\n}\n", f, i, f, i, i)
		}
	}
	return b.Bytes()
}
