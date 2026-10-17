/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ['./index.html', './*.go', './web/src/**/*.js'],
  theme: {
    extend: {
      colors: {
        // Kubernetes brand blue and a dark "cluster" palette.
        k8s: {
          DEFAULT: '#326ce5',
          light: '#5b8cf0',
          dark: '#2453b8',
        },
        cluster: {
          950: '#070b14',
          900: '#0b1220',
          800: '#111a2e',
          700: '#1a2640',
          600: '#26355a',
        },
      },
      fontFamily: {
        mono: ['ui-monospace', 'SFMono-Regular', '"JetBrains Mono"', 'Menlo', 'Consolas', '"Liberation Mono"', 'monospace'],
      },
    },
  },
  plugins: [],
}
