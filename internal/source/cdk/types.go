package cdk

import "strings"

// namespaces maps a CDK module name (aws-cdk-lib/aws-<module>, lowercase, no
// separators) to its CloudFormation service namespace (AWS::<Namespace>::*).
// A module that is not listed has no CloudFormation resources of its own
// (helpers such as s3-deployment, ecs-patterns, lambda-event-sources) and
// its constructs are not reported.
var namespaces = map[string]string{
	"acmpca": "ACMPCA", "amplify": "Amplify", "apigateway": "ApiGateway", "apigatewayv2": "ApiGatewayV2",
	"appconfig": "AppConfig", "appmesh": "AppMesh", "apprunner": "AppRunner", "appsync": "AppSync",
	"athena": "Athena", "autoscaling": "AutoScaling", "backup": "Backup", "batch": "Batch",
	"bedrock": "Bedrock", "certificatemanager": "CertificateManager", "cloudformation": "CloudFormation",
	"cloudfront": "CloudFront", "cloudtrail": "CloudTrail", "cloudwatch": "CloudWatch",
	"codebuild": "CodeBuild", "codecommit": "CodeCommit", "codedeploy": "CodeDeploy",
	"codepipeline": "CodePipeline", "cognito": "Cognito", "config": "Config", "docdb": "DocDB",
	"dynamodb": "DynamoDB", "ec2": "EC2", "ecr": "ECR", "ecs": "ECS", "efs": "EFS", "eks": "EKS",
	"elasticache": "ElastiCache", "elasticloadbalancing": "ElasticLoadBalancing",
	"elasticloadbalancingv2": "ElasticLoadBalancingV2", "elasticsearch": "Elasticsearch",
	"events": "Events", "glue": "Glue", "iam": "IAM", "kinesis": "Kinesis",
	"kinesisfirehose": "KinesisFirehose", "kms": "KMS", "lambda": "Lambda", "logs": "Logs",
	"msk": "MSK", "neptune": "Neptune", "opensearchservice": "OpenSearchService", "pipes": "Pipes",
	"rds": "RDS", "redshift": "Redshift", "route53": "Route53", "s3": "S3", "sagemaker": "SageMaker",
	"scheduler": "Scheduler", "secretsmanager": "SecretsManager", "ses": "SES", "sns": "SNS",
	"sqs": "SQS", "ssm": "SSM", "stepfunctions": "StepFunctions", "timestream": "Timestream",
	"wafv2": "WAFv2",
}

// moduleAlias folds modules that wrap another service's resources into it
// (a Node.js Lambda function is an AWS::Lambda::Function).
var moduleAlias = map[string]string{
	"lambdanodejs": "lambda", "lambdapython": "lambda", "lambdago": "lambda",
	"eventstargets": "", "lambdaeventsources": "", // targets and sources create no resources
}

// classes maps CDK L2 class names whose CloudFormation resource name differs,
// per module. Everything else keeps its name, after dropping the Cfn prefix
// of L1 classes.
var classes = map[string]map[string]string{
	"ec2": {"Vpc": "VPC", "CfnVPC": "VPC", "CfnEIP": "EIP", "CfnNatGateway": "NatGateway",
		"Peer": "", "Port": ""},
	"rds": {"DatabaseInstance": "DBInstance", "DatabaseInstanceReadReplica": "DBInstance",
		"DatabaseCluster": "DBCluster", "ServerlessCluster": "DBCluster", "DatabaseProxy": "DBProxy",
		"SubnetGroup": "DBSubnetGroup", "ParameterGroup": "DBParameterGroup", "CfnDBInstance": "DBInstance",
		"CfnDBCluster": "DBCluster"},
	"dynamodb": {"TableV2": "GlobalTable"},
	"ecs": {"FargateService": "Service", "Ec2Service": "Service", "FargateTaskDefinition": "TaskDefinition",
		"Ec2TaskDefinition": "TaskDefinition", "ExternalService": "Service"},
	"elasticloadbalancingv2": {"ApplicationLoadBalancer": "LoadBalancer", "NetworkLoadBalancer": "LoadBalancer",
		"ApplicationTargetGroup": "TargetGroup", "NetworkTargetGroup": "TargetGroup",
		"ApplicationListener": "Listener", "NetworkListener": "Listener",
		"ApplicationListenerRule": "ListenerRule"},
	"apigateway":   {"LambdaRestApi": "RestApi", "SpecRestApi": "RestApi", "StepFunctionsRestApi": "RestApi"},
	"apigatewayv2": {"HttpApi": "Api", "WebSocketApi": "Api", "HttpStage": "Stage", "WebSocketStage": "Stage"},
	"ssm":          {"StringParameter": "Parameter", "StringListParameter": "Parameter"},
	"route53": {"ARecord": "RecordSet", "AaaaRecord": "RecordSet", "CnameRecord": "RecordSet",
		"TxtRecord": "RecordSet", "MxRecord": "RecordSet", "CaaRecord": "RecordSet", "SrvRecord": "RecordSet",
		"PublicHostedZone": "HostedZone", "PrivateHostedZone": "HostedZone"},
	"lambda": {"DockerImageFunction": "Function", "NodejsFunction": "Function", "PythonFunction": "Function",
		"GoFunction": "Function", "SingletonFunction": "Function", "CfnFunction": "Function"},
	"cloudfront":         {"CloudFrontWebDistribution": "Distribution"},
	"cloudwatch":         {"Metric": "", "Dashboard": "Dashboard"},
	"certificatemanager": {"DnsValidatedCertificate": "Certificate"},
}

// cfnType returns the CloudFormation type of a CDK construct. ok is false
// for modules without CloudFormation resources and for classes that are
// helpers rather than resources (explicitly mapped to "").
func cfnType(module, class string) (string, bool) {
	if a, ok := moduleAlias[module]; ok {
		if a == "" {
			return "", false
		}
		module = a
	}
	ns, ok := namespaces[module]
	if !ok {
		return "", false
	}
	name, mapped := classes[module][class]
	if mapped {
		if name == "" {
			return "", false
		}
	} else {
		name = strings.TrimPrefix(class, "Cfn")
		if name == "" {
			return "", false
		}
	}
	return "AWS::" + ns + "::" + name, true
}
