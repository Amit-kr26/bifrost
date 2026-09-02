import { MCPClientConfigEntry, MCPClientConfigsEditor } from "@/components/mcp/mcpClientConfigsEditor";
import { Button } from "@/components/ui/button";
import { useGetMCPClientsQuery } from "@/lib/store";
import { VirtualMCPToolSpec } from "@/lib/types/virtualMcps";
import { Loader2 } from "lucide-react";
import { useMemo } from "react";

const TOOL_WILDCARD = "*";

const TOOLS_TOOLTIP = (
	<p>
		Pick which of your MCP servers this Virtual MCP exposes and, for each, which tools. After adding a server, select specific tools or
		choose <span className="font-medium">Allow All Tools</span> to expose all of them.
	</p>
);

interface VirtualMcpToolsEditorProps {
	value: VirtualMCPToolSpec[];
	onChange: (specs: VirtualMCPToolSpec[]) => void;
	active: boolean;
}

// Renders the shared MCP-server/tool table for Virtual MCPs. Virtual MCPs key tools by
// client id while the shared editor keys by name, so this adapts between them; an id with
// no matching server (deleted since) is kept by name-falling-back-to-id so edits don't drop it.
export default function VirtualMcpToolsEditor({ value, onChange, active }: VirtualMcpToolsEditorProps) {
	const { data, isLoading, isError, refetch } = useGetMCPClientsQuery({ limit: 1000 }, { skip: !active });
	const clients = useMemo(() => data?.clients ?? [], [data]);
	const nameById = useMemo(() => new Map(clients.map((c) => [c.config.client_id, c.config.name])), [clients]);
	const idByName = useMemo(() => new Map(clients.map((c) => [c.config.name, c.config.client_id])), [clients]);

	const editorValue: MCPClientConfigEntry[] = value.map((spec) => ({
		mcp_client_name: nameById.get(spec.mcp_client_id) ?? spec.mcp_client_id,
		tools_to_execute: spec.tool_names,
	}));

	const handleChange = (entries: MCPClientConfigEntry[]) => {
		onChange(
			entries.map((entry) => ({
				mcp_client_id: idByName.get(entry.mcp_client_name) ?? entry.mcp_client_name,
				tool_names: entry.tools_to_execute ?? [TOOL_WILDCARD],
			})),
		);
	};

	if (isLoading) {
		return (
			<div className="flex items-center justify-center py-10">
				<Loader2 className="text-muted-foreground h-5 w-5 animate-spin" />
			</div>
		);
	}

	if (isError) {
		return (
			<div className="text-destructive flex flex-col items-center gap-3 rounded-md border border-dashed p-6 text-center text-sm">
				Could not load MCP servers.
				<Button variant="outline" size="sm" onClick={() => refetch()}>
					Retry
				</Button>
			</div>
		);
	}

	if (clients.length === 0 && value.length === 0) {
		return (
			<div className="text-muted-foreground rounded-md border border-dashed p-6 text-center text-sm">
				No MCP servers are configured yet. Add one in the MCP Registry first, then it can be exposed here.
			</div>
		);
	}

	return (
		<MCPClientConfigsEditor
			value={editorValue}
			onChange={handleChange}
			clients={clients}
			label="Tools"
			tooltip={TOOLS_TOOLTIP}
			allClientTools
			emptyState={
				<div className="text-muted-foreground rounded-md border border-dashed p-6 text-center text-sm">
					No tools attached yet. Add an MCP server above to expose its tools through this Virtual MCP.
				</div>
			}
		/>
	);
}