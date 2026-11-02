import { createRequire } from 'node:module';

const requireWeb = createRequire(new URL('./apps/web/package.json', import.meta.url));
const js = requireWeb('@eslint/js');
const globals = requireWeb('globals');
const reactHooks = requireWeb('eslint-plugin-react-hooks');
const reactRefresh = requireWeb('eslint-plugin-react-refresh');
const tseslint = requireWeb('typescript-eslint');

export default tseslint.config(
  { ignores: ['dist'] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      'react-refresh/only-export-components': [
        'warn',
        { allowConstantExport: true },
      ],
      '@typescript-eslint/no-explicit-any': 'warn',
      '@typescript-eslint/no-unused-vars': ['warn', { argsIgnorePattern: '^_' }],
    },
  },
);
