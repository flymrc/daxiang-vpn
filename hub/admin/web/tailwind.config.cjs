/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ["./src/**/*.{html,js,svelte,ts}"],
  theme: {
    extend: {
      fontFamily: {
        sans: ["Inter", "IBM Plex Sans", "Segoe UI", "system-ui", "sans-serif"],
        mono: ["IBM Plex Mono", "ui-monospace", "SFMono-Regular", "monospace"],
      },
      colors: {
        hub: {
          bg: "#0b0e13",
          panel: "#0f141b",
          panel2: "#11161d",
          border: "#232c37",
          text: "#e6edf3",
          muted: "#9aa7b4",
          faint: "#677483",
          blue: "#4c8dff",
          green: "#3fb950",
          yellow: "#d6a531",
          red: "#f85149",
        },
      },
    },
  },
  plugins: [],
};
