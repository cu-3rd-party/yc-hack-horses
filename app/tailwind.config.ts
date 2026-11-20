import type { Config } from 'tailwindcss';
import animate from 'tailwindcss-animate';

export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        border: 'hsl(215 25% 20%)',
        background: 'hsl(220 25% 7%)',
        foreground: 'hsl(210 20% 92%)',
        card: 'hsl(220 22% 11%)',
        'card-foreground': 'hsl(210 20% 92%)',
        muted: 'hsl(220 18% 17%)',
        'muted-foreground': 'hsl(215 12% 62%)',
        primary: 'hsl(150 55% 45%)',
        'primary-foreground': 'hsl(220 30% 8%)',
        destructive: 'hsl(0 65% 50%)',
        accent: 'hsl(220 20% 18%)',
      },
      borderRadius: {
        lg: '0.5rem',
      },
    },
  },
  plugins: [animate],
} satisfies Config;
