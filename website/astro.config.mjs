// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightLinksValidator from 'starlight-links-validator';
import starlightLlmsTxt from 'starlight-llms-txt';

const repo = 'https://github.com/pungoyal/dotsync';

export default defineConfig({
	site: 'https://pungoyal.github.io',
	base: '/dotsync',
	trailingSlash: 'always',
	integrations: [
		starlight({
			title: 'dotsync',
			description:
				'Set-and-forget dotfile sync for macOS and Linux. Conflict-safe, secret-aware and automatic.',
			logo: {
				light: './src/assets/logo-light.svg',
				dark: './src/assets/logo-dark.svg',
				replacesTitle: false,
			},
			favicon: '/favicon.svg',
			social: [
				{ icon: 'github', label: 'GitHub', href: repo },
			],
			editLink: { baseUrl: `${repo}/edit/main/website/` },
			lastUpdated: true,
			pagination: true,
			credits: false,
			tableOfContents: { minHeadingLevel: 2, maxHeadingLevel: 3 },
			customCss: [
				'@fontsource-variable/inter',
				'@fontsource-variable/jetbrains-mono',
				'./src/styles/theme.css',
			],
			head: [
				{ tag: 'meta', attrs: { property: 'og:image', content: 'https://pungoyal.github.io/dotsync/og.png' } },
				{ tag: 'meta', attrs: { property: 'og:image:alt', content: 'dotsync: your dotfiles, the same everywhere' } },
				{ tag: 'meta', attrs: { name: 'twitter:card', content: 'summary_large_image' } },
				{ tag: 'meta', attrs: { name: 'theme-color', content: '#0f766e' } },
				{ tag: 'link', attrs: { rel: 'icon', type: 'image/png', sizes: '32x32', href: '/dotsync/favicon-32.png' } },
				{ tag: 'link', attrs: { rel: 'apple-touch-icon', href: '/dotsync/apple-touch-icon.png' } },
				{ tag: 'link', attrs: { rel: 'manifest', href: '/dotsync/site.webmanifest' } },
			],
			expressiveCode: {
				themes: ['github-dark-default', 'github-light-default'],
				styleOverrides: { borderRadius: '0.6rem', codeFontFamily: "'JetBrains Mono Variable', ui-monospace, monospace" },
			},
			components: {
				Footer: './src/components/Footer.astro',
			},
			plugins: [
				starlightLinksValidator({ errorOnRelativeLinks: false }),
				starlightLlmsTxt({
					projectName: 'dotsync',
					description:
						'dotsync keeps dotfiles in sync across macOS and Linux machines through a private git repository, with per-file conflict detection, backups and secret blocking.',
				}),
			],
			sidebar: [
				{
					label: 'Start here',
					items: [
						{ label: 'Introduction', slug: 'start/introduction' },
						{ label: 'Installation', slug: 'start/installation' },
						{ label: 'Quick start', slug: 'start/quick-start', badge: { text: '5 min', variant: 'success' } },
					],
				},
				{
					label: 'Guides',
					items: [
						{ label: 'Manage files and directories', slug: 'guides/manage-files' },
						{ label: 'Add another machine', slug: 'guides/add-a-machine' },
						{ label: 'Resolve conflicts', slug: 'guides/resolve-conflicts' },
						{ label: 'Keep secrets out', slug: 'guides/secrets' },
						{ label: 'Per-OS and per-machine differences', slug: 'guides/differences' },
						{ label: 'Restore a previous version', slug: 'guides/restore' },
						{ label: 'Migrate from another tool', slug: 'guides/migrate' },
						{ label: 'Use with mise', slug: 'guides/mise' },
						{ label: 'The background agent', slug: 'guides/background-agent' },
						{ label: 'Troubleshooting', slug: 'guides/troubleshooting' },
						{ label: 'Uninstall', slug: 'guides/uninstall' },
					],
				},
				{
					label: 'Concepts',
					items: [
						{ label: 'How sync works', slug: 'concepts/how-sync-works' },
						{ label: 'Safety guarantees', slug: 'concepts/safety' },
						{ label: 'Security model', slug: 'concepts/security-model' },
						{ label: 'Design decisions', slug: 'concepts/design-decisions' },
						{ label: 'Comparison with other tools', slug: 'concepts/comparison' },
					],
				},
				{
					label: 'Reference',
					items: [
						{ label: 'Commands', slug: 'reference/commands' },
						{ label: 'Manifest', slug: 'reference/manifest' },
						{ label: 'Machine configuration', slug: 'reference/configuration' },
						{ label: 'Files and locations', slug: 'reference/files' },
						{ label: 'Environment variables', slug: 'reference/environment' },
						{ label: 'Secret detection rules', slug: 'reference/secret-rules' },
						{ label: 'Output and exit codes', slug: 'reference/output' },
					],
				},
				{
					label: 'Project',
					items: [
						{ label: 'FAQ', slug: 'project/faq' },
						{ label: 'Verifying releases', slug: 'project/verifying-releases' },
						{ label: 'Changelog', link: `${repo}/blob/main/CHANGELOG.md`, attrs: { target: '_blank' } },
						{ label: 'Contributing', link: `${repo}/blob/main/CONTRIBUTING.md`, attrs: { target: '_blank' } },
						{ label: 'Security policy', link: `${repo}/security/policy`, attrs: { target: '_blank' } },
					],
				},
			],
		}),
	],
});
