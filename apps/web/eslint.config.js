import js from '@eslint/js';
import prettier from 'eslint-config-prettier';
import svelte from 'eslint-plugin-svelte';
import globals from 'globals';
import ts from 'typescript-eslint';

export default ts.config(
  js.configs.recommended,
  ...ts.configs.recommended,
  ...svelte.configs['flat/recommended'],
  prettier,
  ...svelte.configs['flat/prettier'],
  {
    languageOptions: {
      globals: { ...globals.browser, ...globals.node },
    },
  },
  {
    // eslint-plugin-svelte parses .svelte.ts and .svelte.js with the Svelte
    // parser so that runes are understood outside components. That parser
    // needs to be told to hand the TypeScript through, or every type
    // annotation in a rune module is a syntax error.
    files: ['**/*.svelte', '**/*.svelte.ts', '**/*.svelte.js'],
    languageOptions: {
      parserOptions: { parser: ts.parser },
    },
  },
  {
    ignores: ['dist/', 'node_modules/', '.svelte-kit/'],
  },
);
