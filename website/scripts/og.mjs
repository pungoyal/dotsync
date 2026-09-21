// Renders public/og.png (the social card) from an inline SVG. Run: node scripts/og.mjs
import sharp from 'sharp';
import { readFileSync } from 'node:fs';

const hero = readFileSync(new URL('../src/assets/hero-dark.svg', import.meta.url), 'utf8')
	.replace(/^<svg[^>]*>/, '')
	.replace(/<\/svg>\s*$/, '');

const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="630" viewBox="0 0 1200 630">
  <defs>
    <radialGradient id="bg" cx="75%" cy="30%" r="80%">
      <stop offset="0" stop-color="#0b2e2b"/><stop offset="1" stop-color="#0f1520"/>
    </radialGradient>
  </defs>
  <rect width="1200" height="630" fill="url(#bg)"/>
  <g transform="translate(80 96)">
    <g transform="scale(2.1)" fill="none">
      <path d="M26 16a10 10 0 0 1-17.3 6.8" stroke="#14b8a6" stroke-width="2.6" stroke-linecap="round"/>
      <path d="M6 16A10 10 0 0 1 23.3 9.2" stroke="#99f6e4" stroke-width="2.6" stroke-linecap="round"/>
      <path d="M23.8 4.6v5.1h-5.1" stroke="#99f6e4" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"/>
      <path d="M8.2 27.4v-5.1h5.1" stroke="#14b8a6" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"/>
      <circle cx="16" cy="16" r="3.2" fill="#e8edf2"/>
    </g>
    <text x="84" y="50" font-family="Inter, Helvetica, Arial, sans-serif" font-size="46" font-weight="700" fill="#e8edf2">dotsync</text>
    <text x="0" y="190" font-family="Inter, Helvetica, Arial, sans-serif" font-size="60" font-weight="750" fill="#ffffff" letter-spacing="-1.5">Your dotfiles,</text>
    <text x="0" y="262" font-family="Inter, Helvetica, Arial, sans-serif" font-size="60" font-weight="750" fill="#ffffff" letter-spacing="-1.5">the same everywhere.</text>
    <text x="0" y="330" font-family="Inter, Helvetica, Arial, sans-serif" font-size="26" fill="#c3ccd6">Automatic, conflict-safe dotfile sync</text>
    <text x="0" y="366" font-family="Inter, Helvetica, Arial, sans-serif" font-size="26" fill="#c3ccd6">for macOS and Linux.</text>
    <text x="0" y="440" font-family="JetBrains Mono, Menlo, monospace" font-size="22" fill="#14b8a6">pungoyal.github.io/dotsync</text>
  </g>
  <svg x="690" y="110" width="460" height="372" viewBox="0 0 520 420">${hero}</svg>
</svg>`;

await sharp(Buffer.from(svg)).png({ compressionLevel: 9 }).toFile(new URL('../public/og.png', import.meta.url).pathname);
console.log('wrote public/og.png');
