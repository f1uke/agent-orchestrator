import { useEffect, useRef, useState } from "react";
import { cn } from "../lib/utils";
import { Input } from "./ui/input";

type BranchComboboxProps = {
	branches: string[];
	value: string;
	onChange: (value: string) => void;
	id?: string;
	placeholder?: string;
};

export function BranchCombobox({ branches, value, onChange, id, placeholder }: BranchComboboxProps) {
	const [query, setQuery] = useState(value);
	const [open, setOpen] = useState(false);
	const [hasTyped, setHasTyped] = useState(false);
	const containerRef = useRef<HTMLDivElement>(null);

	useEffect(() => {
		setQuery(value);
	}, [value]);

	useEffect(() => {
		if (!open) return;
		const handlePointerDown = (event: MouseEvent) => {
			if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
				setOpen(false);
				setHasTyped(false);
			}
		};
		document.addEventListener("mousedown", handlePointerDown);
		return () => document.removeEventListener("mousedown", handlePointerDown);
	}, [open]);

	const needle = query.trim().toLowerCase();
	const filtered = hasTyped && needle ? branches.filter((branch) => branch.toLowerCase().includes(needle)) : branches;

	const selectBranch = (branch: string) => {
		setQuery(branch);
		onChange(branch);
		setOpen(false);
		setHasTyped(false);
	};

	return (
		<div ref={containerRef} className="relative">
			<Input
				id={id}
				autoComplete="off"
				placeholder={placeholder}
				value={query}
				onFocus={(event) => {
					event.target.select();
					setOpen(true);
					setHasTyped(false);
				}}
				onClick={(event) => {
					event.currentTarget.select();
					setOpen(true);
					setHasTyped(false);
				}}
				onChange={(event) => {
					const next = event.target.value;
					setQuery(next);
					onChange(next);
					setOpen(true);
					setHasTyped(true);
				}}
				onBlur={() => {
					setOpen(false);
					setHasTyped(false);
				}}
				onKeyDown={(event) => {
					if (event.key === "Escape") {
						setOpen(false);
						setHasTyped(false);
					}
				}}
			/>
			{open && filtered.length > 0 && (
				<ul
					role="listbox"
					className="absolute z-50 mt-1 max-h-48 w-full overflow-y-auto rounded-md border border-border bg-popover py-1 text-popover-foreground shadow-md"
				>
					{filtered.map((branch) => (
						<li key={branch}>
							<button
								type="button"
								role="option"
								aria-selected={branch === value}
								className={cn(
									"block w-full truncate px-3 py-1.5 text-left text-[13px] hover:bg-surface hover:text-foreground",
									branch === value ? "text-foreground" : "text-muted-foreground",
								)}
								onMouseDown={(event) => event.preventDefault()}
								onClick={() => selectBranch(branch)}
							>
								{branch}
							</button>
						</li>
					))}
				</ul>
			)}
		</div>
	);
}
