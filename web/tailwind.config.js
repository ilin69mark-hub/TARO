/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ["./app/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}"],
  theme: {
    extend: {
      fontFamily: {
        // см. 05-design/03-typography.md: Display — Cormorant, Body/Accent — Inter
        display: ["var(--font-display)", "serif"],
        sans: ["var(--font-body)", "system-ui", "sans-serif"],
      },
      colors: {
        // Палитра из docs/project-book/05-design/02-palette.md
        deep: "#0B0B14",
        card: "#151524",
        elev: "#1E1E32",
        gold: "#D4AF37",
        goldsoft: "#F1D97B",
        violet: "#7C5CFF",
        mist: "#A8A8C0",
        paper: "#F5F0E6",
      },
    },
  },
  plugins: [],
};
