const { awscdk } = require('projen');
const project = new awscdk.AwsCdkTypeScriptApp({
  cdkVersion: '2.90.0',
  cdkVersionPinning: false,
  defaultReleaseBranch: 'main',
  name: 'egress-proxy',
  description: `Providing controlled internet access through centralised proxy servers
  using AWS Fargate and PrivateLink`,
  authorName: 'Norman Khine',
  authorEmail: 'norman@khine.net',
  repository: 'https://github.com/nkhine/serverless-patterns/egress-proxy',
  entrypoint: 'bin/main.ts',
  licensed: false,
  gitignore: ['!lib/*.ts', '!bin/*.ts'],
  deps: [
    'yaml',
    'cdk-aws-lambda-powertools-layer',
  ] /* Runtime dependencies of this module. */,
  devDeps: ['@types/node', 'cdk-dia'] /* Build dependencies for this module. */,
  context: {},
  dependabot: false,
  buildWorkflow: false,
  releaseWorkflow: false,
  github: false,
  jest: false,
  appEntrypoint: 'main.ts',
  buildCommand: 'make',
  clobber: false,
  srcdir: 'bin',
});

project.addTask('gen-dia', {
  cwd: './docs',
  exec: `
    npx cdk-dia --tree ../cdk.out/tree.json  \
		--include EgressProxy-CICD-Stack \
		--include cross-region-stack-000000000000:eu-west-2 \
		--include EgressProxy-CICD-Stack/eu-west-1-Egress/ProxyStack \
		--include EgressProxy-CICD-Stack/eu-west-1-Egress/ProxyPipeline \
		--include EgressProxy-CICD-Stack/eu-west-1-Spoke/SpokeStack222222222222 \
		--include EgressProxy-CICD-Stack/eu-west-1-Spoke/SpokeStack333333333333
  `,
});

project.synth();
