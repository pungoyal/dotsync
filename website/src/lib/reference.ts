/**
 * The command, secret-rule and default-value reference, generated from the Go source by
 * internal/dotsync/reference_test.go. Components read it from here so the docs can't drift from
 * the code.
 */
import data from '../data/reference.json';

export type Flag = { names: string[]; value?: string; repeats?: boolean; default?: string; usage: string };
export type Command = { name: string; aliases?: string[]; args?: string; usage: string; summary: string; flags: Flag[] };

export const reference = data as typeof data & {
	commands: { title: string; commands: Command[] }[];
};

export const commands: Command[] = reference.commands.flatMap((g) => g.commands);

export function command(name: string): Command {
	const c = commands.find((c) => c.name === name);
	if (!c) throw new Error(`reference.json has no command "${name}"`);
	return c;
}
