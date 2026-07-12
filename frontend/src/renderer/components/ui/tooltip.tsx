import * as TooltipPrimitive from "@radix-ui/react-tooltip";
import { cn } from "../../lib/utils";

export const TooltipProvider = TooltipPrimitive.Provider;
export const TooltipTrigger = TooltipPrimitive.Trigger;

/**
 * Tooltip root. Defaults `disableHoverableContent` so a tooltip behaves as a passive
 * hint: moving the pointer off the trigger and onto the bubble lets it dismiss instead
 * of Radix's default "hoverable content" grace area keeping it stuck open. That grace
 * area is tracked at the document level, so `pointer-events: none` on the popper alone
 * can't defeat it — this prop is what actually lets the bubble dismiss. A caller can
 * still opt back in by passing `disableHoverableContent={false}`.
 */
export function Tooltip({
	disableHoverableContent = true,
	...props
}: React.ComponentProps<typeof TooltipPrimitive.Root>) {
	return <TooltipPrimitive.Root disableHoverableContent={disableHoverableContent} {...props} />;
}

export function TooltipContent({
	className,
	sideOffset = 6,
	...props
}: React.ComponentPropsWithoutRef<typeof TooltipPrimitive.Content>) {
	return (
		<TooltipPrimitive.Portal>
			<TooltipPrimitive.Content
				className={cn(
					// A tooltip is a passive hint: keep the popper non-interactive so moving the
					// pointer onto the bubble lets it dismiss (it can't hold its own open state)
					// and its copy can't be drag-selected. See the matching scoped popper-wrapper
					// rule in styles.css (the wrapper is the outermost hit target).
					"pointer-events-none select-none z-50 rounded-md border border-border bg-popover px-2 py-1 text-xs text-popover-foreground shadow-md",
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
