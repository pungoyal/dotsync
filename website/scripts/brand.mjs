// Generates every brand asset from the master SVGs in assets/brand/ (themselves produced by
// assets/brand/generate_icon.py). Run from website/: npm run brand
import sharp from 'sharp';
import { mkdirSync, readFileSync, writeFileSync, copyFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..', '..');
const brand = join(root, 'assets', 'brand');
const icon = readFileSync(join(brand, 'icon.svg'));
const markDark = readFileSync(join(brand, 'mark-dark.svg'), 'utf8');
const out = (...p) => {
	const f = join(root, ...p);
	mkdirSync(dirname(f), { recursive: true });
	return f;
};
const png = (svg, size) => sharp(svg, { density: Math.max(72, (size / 64) * 72 * 2) }).resize(size, size).png({ compressionLevel: 9 });

// 1. Icon PNGs, for anyone who needs a raster (READMEs elsewhere, launchers, slides).
for (const size of [16, 32, 48, 64, 128, 180, 192, 256, 512, 1024]) {
	await png(icon, size).toFile(out('assets', 'brand', 'png', `icon-${size}.png`));
}

// 2. Documentation site: favicon set, web app manifest, header logos.
copyFileSync(join(brand, 'icon.svg'), out('website', 'public', 'favicon.svg'));
await png(icon, 32).toFile(out('website', 'public', 'favicon-32.png'));
await png(icon, 180).toFile(out('website', 'public', 'apple-touch-icon.png'));
await png(icon, 192).toFile(out('website', 'public', 'icon-192.png'));
await png(icon, 512).toFile(out('website', 'public', 'icon-512.png'));
writeFileSync(
	out('website', 'public', 'site.webmanifest'),
	JSON.stringify(
		{
			name: 'dotsync',
			short_name: 'dotsync',
			description: 'Set-and-forget dotfile sync for macOS and Linux.',
			start_url: '/dotsync/',
			scope: '/dotsync/',
			display: 'standalone',
			background_color: '#0f1520',
			theme_color: '#0f766e',
			icons: [
				{ src: '/dotsync/icon-192.png', sizes: '192x192', type: 'image/png' },
				{ src: '/dotsync/icon-512.png', sizes: '512x512', type: 'image/png' },
				{ src: '/dotsync/favicon.svg', sizes: 'any', type: 'image/svg+xml' },
			],
		},
		null,
		2,
	) + '\n',
);
copyFileSync(join(brand, 'mark-dark.svg'), out('website', 'src', 'assets', 'logo-dark.svg'));
copyFileSync(join(brand, 'mark-light.svg'), out('website', 'src', 'assets', 'logo-light.svg'));

// 3. The CLI embeds a 256px icon for desktop notifications.
await png(icon, 256).toFile(out('internal', 'dotsync', 'assets', 'icon.png'));

// 4. Social cards: docs link previews (og.png) and the GitHub repository preview.
const iconInner = icon.toString().replace(/^[\s\S]*?<svg[^>]*>/, '').replace(/<\/svg>\s*$/, '');
const card = (w, h, { tagline, url }) => `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}">
  <defs>
    <radialGradient id="glow" cx="78%" cy="30%" r="75%">
      <stop offset="0" stop-color="#0b2e2b"/><stop offset="1" stop-color="#0f1520"/>
    </radialGradient>
  </defs>
  <rect width="${w}" height="${h}" fill="url(#glow)"/>
  <svg x="${w - 96 - 300}" y="${(h - 300) / 2}" width="300" height="300" viewBox="0 0 64 64"><g fill="none">${iconInner}</g></svg>
  <g font-family="Inter, Helvetica, Arial, sans-serif">
    <svg x="96" y="${h / 2 - 170}" width="56" height="56" viewBox="0 0 64 64"><g fill="none">${markDark.replace(/^<svg[^>]*>/, '').replace(/<\/svg>\s*$/, '')}</g></svg>
    <text x="166" y="${h / 2 - 128}" font-size="40" font-weight="700" fill="#e8edf2">dotsync</text>
    <text x="96" y="${h / 2 - 20}" font-size="60" font-weight="750" fill="#ffffff" letter-spacing="-1.5">Your dotfiles,</text>
    <text x="96" y="${h / 2 + 52}" font-size="60" font-weight="750" fill="#ffffff" letter-spacing="-1.5">the same everywhere.</text>
    <text x="96" y="${h / 2 + 118}" font-size="26" fill="#c3ccd6">${tagline}</text>
    <text x="96" y="${h / 2 + 176}" font-family="JetBrains Mono, Menlo, monospace" font-size="22" fill="#2dd4bf">${url}</text>
  </g>
</svg>`;
const tagline = 'Automatic, conflict-safe dotfile sync for macOS and Linux.';
await sharp(Buffer.from(card(1200, 630, { tagline, url: 'pungoyal.github.io/dotsync' })))
	.png({ compressionLevel: 9 })
	.toFile(out('website', 'public', 'og.png'));
await sharp(Buffer.from(card(1280, 640, { tagline, url: 'github.com/pungoyal/dotsync' })))
	.png({ compressionLevel: 9 })
	.toFile(out('assets', 'brand', 'social-preview.png'));

console.log('brand assets written');
