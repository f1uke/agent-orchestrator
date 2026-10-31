import { type CSSProperties, useRef, useState } from "react";
import { type ChatImage, imageSrc } from "../lib/chat-images";
import { MediaLightbox, type MediaItem, MediaThumb } from "./MediaLightbox";

export function ChatImages({
	images,
	name,
	thumb,
}: {
	images: readonly ChatImage[];
	name: (index: number) => string;
	thumb: CSSProperties;
}) {
	const [open, setOpen] = useState<number | null>(null);
	const trigger = useRef<HTMLElement | null>(null);
	const items: MediaItem[] = images.map((image, i) => ({
		id: String(i),
		filename: name(i),
		mime: image.mediaType,
		src: "",
		dataUrl: imageSrc(image),
	}));
	return (
		<>
			{items.map((item, i) => (
				<MediaThumb
					item={item}
					key={item.id}
					onOpen={(el) => {
						trigger.current = el;
						setOpen(i);
					}}
					style={thumb}
				/>
			))}
			{open !== null ? (
				<MediaLightbox
					index={open}
					items={items}
					onClose={() => setOpen(null)}
					onIndexChange={setOpen}
					triggerRef={trigger}
				/>
			) : null}
		</>
	);
}

// The minimum keeps a very wide or tall strip clickable.
export const READ_THUMB: CSSProperties = {
	display: "block",
	maxWidth: 240,
	maxHeight: 240,
	minWidth: 24,
	minHeight: 24,
	objectFit: "cover",
	borderRadius: 8,
	border: "1px solid var(--border-1)",
};

export const BUBBLE_THUMB: CSSProperties = {
	display: "block",
	height: 96,
	maxWidth: 240,
	objectFit: "cover",
	borderRadius: 8,
	border: "1px solid var(--border-1)",
};
