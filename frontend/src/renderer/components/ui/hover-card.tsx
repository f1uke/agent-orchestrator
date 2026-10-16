import { HoverCard as HoverCardPrimitive } from "radix-ui";
import { cn } from "../../lib/utils";

export const HoverCard = HoverCardPrimitive.Root;
export const HoverCardTrigger = HoverCardPrimitive.Trigger;

export function HoverCardContent({
	className,
	align = "start",
	sideOffset = 6,
	...props
}: React.ComponentPropsWithoutRef<typeof HoverCardPrimitive.Content>) {
	return (
		<HoverCardPrimitive.Portal>
			<HoverCardPrimitive.Content
				align={align}
				sideOffset={sideOffset}
				collisionPadding={8}
				className={cn(
					"z-[60] w-max max-w-[340px] min-w-[220px] origin-(--radix-hover-card-content-transform-origin) rounded-md border border-border bg-popover p-3 text-popover-foreground shadow-md outline-none",
					"data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:animate-in data-[state=open]:fade-in-0",
					className,
				)}
				{...props}
			/>
		</HoverCardPrimitive.Portal>
	);
}
