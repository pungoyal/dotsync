/** Prefixes a site-relative path with the base the site is served from (/dotsync). */
export function withBase(path: string): string {
	return import.meta.env.BASE_URL.replace(/\/$/, '') + path;
}
