import * as TooltipPrimitive from "@radix-ui/react-tooltip";
import { cn } from "../../lib/utils";

export const TooltipProvider = TooltipPrimitive.Provider;
export const Tooltip = TooltipPrimitive.Root;
export const TooltipTrigger = TooltipPrimitive.Trigger;

export function TooltipContent({
	className,
	sideOffset = 6,
	...props
}: React.ComponentPropsWithoutRef<typeof TooltipPrimitive.Content>) {
	return (
		<TooltipPrimitive.Portal>
			<TooltipPrimitive.Content
				className={cn(
					"z-50 rounded-md border border-border bg-popover px-2 py-1 text-xs text-popover-foreground shadow-md",
					className,
				)}
				sideOffset={sideOffset}
				{...props}
			/>
		</TooltipPrimitive.Portal>
	);
}

/**
 * A one-liner tooltip: hover copy attached to a single trigger element. Composes the
 * shared Tooltip primitives so delay/styling stay identical to the rest of the app.
 * `children` must be a single element that forwards a ref (Radix `asChild`); to give a
 * disabled control a tooltip, wrap it in a `<span>` since disabled elements swallow
 * hover events.
 */
export function SimpleTooltip({
	label,
	side = "top",
	children,
}: {
	label: React.ReactNode;
	side?: React.ComponentProps<typeof TooltipContent>["side"];
	children: React.ReactNode;
}) {
	return (
		<Tooltip>
			<TooltipTrigger asChild>{children}</TooltipTrigger>
			<TooltipContent side={side}>{label}</TooltipContent>
		</Tooltip>
	);
}
