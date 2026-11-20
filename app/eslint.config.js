import tseslint from 'typescript-eslint';
import importPlugin from 'eslint-plugin-import';

// Границы FSD: импорт только сверху вниз
// (app -> pages -> widgets -> features -> entities -> shared).
// Нижний слой не может импортировать вышележащий.
const layers = ['pages', 'widgets', 'features', 'entities', 'shared'];

const zones = [];
layers.forEach((layer, i) => {
  layers.slice(0, i).forEach((higher) => {
    zones.push({
      target: './src/' + layer,
      from: './src/' + higher,
      message:
        'Нарушение FSD-границ: слой "' + layer + '" не может импортировать "' + higher + '"',
    });
  });
});

export default tseslint.config(
  {
    ignores: ['node_modules', 'dist', 'coverage'],
  },
  {
    files: ['**/*.{ts,tsx}'],
    extends: [...tseslint.configs.recommended],
    plugins: { import: importPlugin },
    settings: {
      'import/resolver': {
        typescript: {
          project: './tsconfig.json',
          alwaysTryTypes: true,
        },
      },
    },
    rules: {
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
      'no-console': 'warn',
      'import/no-restricted-paths': [
        'error',
        {
          zones,
        },
      ],
    },
  },
);
