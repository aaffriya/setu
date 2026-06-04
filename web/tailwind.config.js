/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{svelte,ts,js}'],
  // Follow the OS appearance. `dark:` is the escape hatch for the few accents
  // the token system can't express; the bulk flips via the CSS vars below.
  darkMode: 'media',
  theme: {
    extend: {
      colors: {
        // Theme-aware neutral, driven by --ink in app.css. Used at varying
        // opacities for text, fills and borders so one token covers both modes
        // (white on dark, slate-900 on light). `panel` is a solid surface.
        ink: 'rgb(var(--ink) / <alpha-value>)',
        panel: 'rgb(var(--panel) / <alpha-value>)',
      },
      fontFamily: {
        sans: [
          'ui-sans-serif',
          'system-ui',
          '-apple-system',
          'Segoe UI',
          'Roboto',
          'Helvetica Neue',
          'sans-serif',
        ],
      },
    },
  },
  plugins: [],
}
