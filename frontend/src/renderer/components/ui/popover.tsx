import { Popover as PopoverPrimitive } from "radix-ui";
import { cn } from "../../lib/utils";

export const Popover = PopoverPrimitive.Root;
export const PopoverTrigger = PopoverPrimitive.Trigger;
export const PopoverAnchor = PopoverPrimitive.Anchor;

/**
 * Popover content: an interactive floating panel anchored to its trigger. Unlike
 * the passive Tooltip popper, this holds its own open state, is focusable, and
 * its copy is selectable. Styling matches the app's shared popover vocabulary
 * (border + bg-popover + shadow) and the enter/exit animation used by Select.
 */
export function PopoverContent({
	className,
	align = "center",
	sideOffset = 8,
	...props
}: React.ComponentPropsWithoutRef<typeof PopoverPrimitive.Content>) {
	return (
		<PopoverPrimitive.Portal>
			<PopoverPrimitive.Content
				align={align}
				sideOffset={sideOffset}
				className={cn(
					"z-50 w-72 origin-(--radix-popover-content-transform-origin) rounded-md border border-border bg-popover p-1 text-popover-foreground shadow-md outline-none",
					"data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95 data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95",
					"data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2",
					className,
				)}
				{...props}
			/>
		</PopoverPrimitive.Portal>
	);
}
