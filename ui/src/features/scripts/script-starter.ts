// The starter body for a newly created script. There is no kind to branch on any more —
// every script is reached the same way, by whoever imports it.
export const STARTER_SOURCE = `// Reached by \`import { greeting } from "#/scripts/<name>"\` from this collection, or
// \`import { greeting } from "@/<path>"\` from anywhere in the workspace. In expression
// position (a request body/metadata) an \`import\` statement is a parse error there — use
// \`require("#/scripts/<name>")\` instead. The default export is what a middleware
// attachment or a test run calls; the named export is for everyone else's import.
export function greeting(name: string): string {
  return \`hello, \${name}\`;
}

export default greeting;
`;
