import * as React from "react";
import { cn } from "../../lib/utils";

export const Textarea = React.forwardRef<HTMLTextAreaElement, React.ComponentProps<"textarea">>(
	({ className, ...props }, ref) => (
		<textarea
			ref={ref}
			className={cn(
				"min-h-24 w-full rounded-md border border-input bg-transparent px-2.5 py-2 text-[13px] text-foreground placeholder:text-passive focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak",
				className,
			)}
			{...props}
		/>
	),
);
Textarea.displayName = "Textarea";
