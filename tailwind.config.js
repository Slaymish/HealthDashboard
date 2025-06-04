module.exports = {
  content: [
    './views/**/*.tmpl'
  ],
  darkMode: 'class',
  theme: {
    extend: {
      fontFamily: {
        sans: ['Inter', 'ui-sans-serif', 'system-ui', 'sans-serif']
      }
    }
  },
  plugins: [require('@tailwindcss/typography')]
};
