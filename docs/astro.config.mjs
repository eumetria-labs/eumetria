// @ts-check
// The documentation site: Starlight (Astro), built from the Markdown in
// src/content/docs and published to GitHub Pages by
// .github/workflows/docs.yml. Links between pages are site-absolute with
// the base path (/eumetria/…), so starlight-links-validator can check them;
// a custom domain later means base: '/' and replacing /eumetria/ in them.
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightLinksValidator from 'starlight-links-validator';

export default defineConfig({
	site: 'https://eumetria-labs.github.io',
	base: '/eumetria',
	integrations: [
		starlight({
			title: 'Eumetria',
			description: 'Your nutrition, training and sleep in one self-hosted dashboard.',
			logo: { src: './src/assets/mark.svg' },
			favicon: '/favicon.svg',
			customCss: ['./src/styles/eumetria.css'],
			social: [
				{ icon: 'github', label: 'GitHub', href: 'https://github.com/eumetria-labs/eumetria' },
				{ icon: 'discord', label: 'Discord', href: 'https://discord.gg/dqJ7JPdsQ4' },
			],
			editLink: { baseUrl: 'https://github.com/eumetria-labs/eumetria/edit/main/docs/' },
			lastUpdated: true,
			// A link to a page or heading that isn't there fails the build.
			plugins: [starlightLinksValidator()],
			sidebar: [
				{
					label: 'Getting started',
					items: [
						{ label: 'Quick Start', slug: 'start/quick-start' },
						{ label: 'First steps', slug: 'start/first-steps' },
					],
				},
				{
					label: 'Install on a server',
					items: [
						{ label: 'Docker Compose and nginx', slug: 'install/server' },
						{ label: 'Configuration', slug: 'install/configuration' },
					],
				},
				{
					label: 'Connect your data',
					items: [
						{ label: 'Yazio', slug: 'connect/yazio' },
						{ label: 'Fitbit and Pixel Watch', slug: 'connect/google-health' },
						{ label: 'Claude: review and coach', slug: 'connect/claude' },
						{ label: 'MCP: ask Claude Code', slug: 'connect/mcp' },
					],
				},
				{
					label: 'Features',
					items: [
						{ label: 'The dashboard', slug: 'features/dashboard' },
						{ label: 'Nutrition', slug: 'features/nutrition' },
						{ label: 'Body and sleep', slug: 'features/body-sleep' },
						{ label: 'Trainings', slug: 'features/trainings' },
						{ label: 'Personal bests', slug: 'features/personal-bests' },
						{ label: 'Notes and whiteboard photos', slug: 'features/notes' },
						{ label: 'Check-in, phases and events', slug: 'features/check-in-phases' },
						{ label: 'Insights', slug: 'features/insights' },
						{ label: 'Export, report and sharing', slug: 'features/export-share' },
						{ label: 'On your phone', slug: 'features/phone' },
					],
				},
				{
					label: 'Accounts and security',
					items: [
						{ label: 'Accounts and roles', slug: 'security/accounts' },
						{ label: 'Sign-in and second factors', slug: 'security/sign-in' },
					],
				},
				{
					label: 'Operations',
					items: [
						{ label: 'Backups and restore', slug: 'operations/backups' },
						{ label: 'Maintenance', slug: 'operations/maintenance' },
					],
				},
				{
					label: 'Project',
					items: [
						{ label: 'Local development', slug: 'project/development' },
						{ label: 'Unofficial API and credits', slug: 'project/disclaimer' },
					],
				},
			],
		}),
	],
});
