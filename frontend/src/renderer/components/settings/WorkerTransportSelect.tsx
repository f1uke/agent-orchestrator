import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { WORKER_TRANSPORT_OPTIONS, type WorkerTransport } from "./worker-transport";

const INHERIT = "__inherit__";

type Props = { id: string } & (
	| { scope: "global"; value: WorkerTransport; onChange: (value: WorkerTransport) => void }
	| { scope: "project"; value: WorkerTransport | ""; onChange: (value: WorkerTransport | "") => void }
);

export function WorkerTransportSelect(props: Props) {
	const { id } = props;
	const value = props.scope === "project" ? props.value || INHERIT : props.value;
	const onValueChange = (v: string) => {
		if (props.scope === "project") props.onChange(v === INHERIT ? "" : (v as WorkerTransport));
		else props.onChange(v as WorkerTransport);
	};
	return (
		<Select value={value} onValueChange={onValueChange}>
			<SelectTrigger id={id} className="h-8 w-full max-w-[340px] text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				{props.scope === "project" && <SelectItem value={INHERIT}>Inherit global default</SelectItem>}
				{WORKER_TRANSPORT_OPTIONS.map((o) => (
					<SelectItem key={o.value} value={o.value}>
						{o.label}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}
