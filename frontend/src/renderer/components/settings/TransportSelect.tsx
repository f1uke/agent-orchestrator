import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { TRANSPORT_OPTIONS, type Transport } from "./transport";

const INHERIT = "__inherit__";

type Props = { id: string } & (
	| { scope: "global"; value: Transport; onChange: (value: Transport) => void }
	| { scope: "project"; value: Transport | ""; onChange: (value: Transport | "") => void }
);

export function TransportSelect(props: Props) {
	const { id } = props;
	const value = props.scope === "project" ? props.value || INHERIT : props.value;
	const onValueChange = (v: string) => {
		if (props.scope === "project") props.onChange(v === INHERIT ? "" : (v as Transport));
		else props.onChange(v as Transport);
	};
	return (
		<Select value={value} onValueChange={onValueChange}>
			<SelectTrigger id={id} className="h-8 w-full max-w-[340px] text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				{props.scope === "project" && <SelectItem value={INHERIT}>Inherit global default</SelectItem>}
				{TRANSPORT_OPTIONS.map((o) => (
					<SelectItem key={o.value} value={o.value}>
						{o.label}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}
