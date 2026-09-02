import { Button } from "@/components/ui/button";
import { ComboboxSelect } from "@/components/ui/combobox";
import { Label } from "@/components/ui/label";
import { MultiSelect } from "@/components/ui/multiSelect";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { MCPClient } from "@/lib/types/mcp";
import { Info, Trash2 } from "lucide-react";
import { ReactNode } from "react";
import { toast } from "sonner";

// One MCP server's tool grant. tools_to_execute: ["*"] = all tools (incl. future),
// [] = none, a named list = specific. Keyed by client name to match the VK config shape.
export interface MCPClientConfigEntry {
	id?: number;
	mcp_client_name: string;
	tools_to_execute?: string[];
}

interface MCPClientConfigsEditorProps {
	value: MCPClientConfigEntry[];
	onChange: (next: MCPClientConfigEntry[]) => void;
	clients: MCPClient[];
	// Show the "available by default" note for allow_by_default servers. VK-specific.
	showDefaultsNote?: boolean;
	label?: string;
	tooltip?: ReactNode;
	// Rendered below the add-dropdown when nothing is configured yet.
	emptyState?: ReactNode;
	// Offer every tool the server exposes, ignoring the client's own tools_to_execute whitelist.
	// Virtual MCPs pick from the full tool set; the VK sheet keeps the client-scoped default.
	allClientTools?: boolean;
}

const DEFAULT_TOOLTIP = (
	<p>
		Configure which MCP servers this virtual key can use and their allowed tools. Leaving this section empty blocks all MCP tools. After
		adding an MCP server, you must select specific tools or choose <span className="font-medium">Allow All Tools</span> to grant tool
		access.
	</p>
);

// Editor for per-client tool grants: an add-dropdown plus a table of client rows with a
// tool multi-select and a remove button. Extracted from the VK sheet so the Virtual MCP
// wizard reuses the same UI.
export function MCPClientConfigsEditor({
	value,
	onChange,
	clients,
	showDefaultsNote = false,
	label = "MCP Server Configurations",
	tooltip = DEFAULT_TOOLTIP,
	emptyState,
	allClientTools = false,
}: MCPClientConfigsEditorProps) {
	const handleAddMCPClient = (mcpClientName: string) => {
		if (value.some((config) => config.mcp_client_name === mcpClientName)) {
			toast.error("This MCP server is already configured");
			return;
		}
		onChange([...value, { mcp_client_name: mcpClientName, tools_to_execute: ["*"] }]);
	};

	const handleRemoveMCPClient = (index: number) => {
		onChange(value.filter((_, i) => i !== index));
	};

	const handleUpdateMCPConfig = (index: number, field: keyof MCPClientConfigEntry, next: unknown) => {
		const updated = [...value];
		updated[index] = { ...updated[index], [field]: next };
		onChange(updated);
	};

	// Servers not yet configured, as searchable options keyed by name.
	const addableOptions = clients
		.filter((client) => client.config.name && !value.some((config) => config.mcp_client_name === client.config.name))
		.map((client) => ({ label: client.config.name, value: client.config.name }));

	if (clients.length === 0 && value.length === 0) return null;

	return (
		<div className="mt-6 space-y-2">
			<div className="flex items-center gap-2">
				<Label className="text-sm font-medium">{label}</Label>
				<TooltipProvider>
					<Tooltip>
						<TooltipTrigger asChild>
							<span>
								<Info className="text-muted-foreground h-3 w-3" />
							</span>
						</TooltipTrigger>
						<TooltipContent>{tooltip}</TooltipContent>
					</Tooltip>
				</TooltipProvider>
			</div>

			{/* MCP servers allowed by default, excluding explicitly overridden ones */}
			{showDefaultsNote &&
				(() => {
					const defaultMCPClients = clients.filter(
						(client) => client.config.allow_by_default && !value.some((config) => config.mcp_client_name === client.config.name),
					);
					return defaultMCPClients.length > 0 ? (
						<div className="text-muted-foreground rounded-md border p-3 text-xs">
							<div className="flex items-start gap-1.5">
								<Info className="mt-0.5 h-3 w-3 shrink-0" />
								<span>
									The following MCP servers are available to this key by default with all tools enabled on that client:{" "}
									<span className="text-foreground font-medium">{defaultMCPClients.map((c) => c.config.name).join(", ")}</span>. Adding an
									explicit config for any of them below will override the all-tools default for this key.
								</span>
							</div>
						</div>
					) : null;
				})()}

			{/* Add MCP Server Dropdown. Searchable: some deployments have thousands of servers. */}
			{clients.length > 0 && (
				<ComboboxSelect
					options={addableOptions}
					value={null}
					onValueChange={(name) => name && handleAddMCPClient(name)}
					placeholder="Select an MCP server to add"
					searchPlaceholder="Search MCP servers..."
					emptyMessage="All MCP servers configured"
					hideClear
					className="w-full"
					data-testid="mcp-server-add-select"
				/>
			)}

			{value.length === 0 && emptyState}

			{/* MCP Configurations Table */}
			{value.length > 0 && (
				<div className="rounded-md border">
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>MCP Server</TableHead>
								<TableHead>Allowed Tools</TableHead>
								<TableHead className="w-[50px]"></TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{value.map((config, index) => {
								const mcpClient = clients.find((client) => client.config.name === config.mcp_client_name);

								// Handle new wildcard semantics for client-level filtering
								const clientToolsToExecute = mcpClient?.config?.tools_to_execute;
								let availableTools: { name: string; description?: string }[] = [];

								if (allClientTools) {
									// Offer every tool the server exposes, independent of the client's own whitelist.
									availableTools = mcpClient?.tools || [];
								} else if (!clientToolsToExecute || clientToolsToExecute.length === 0) {
									// nil/undefined or empty array - no tools available from client config
									availableTools = [];
								} else if (clientToolsToExecute.includes("*")) {
									// Wildcard - all tools available
									availableTools = mcpClient?.tools || [];
								} else {
									// Specific tools listed
									availableTools = (mcpClient?.tools || []).filter((tool) => clientToolsToExecute.includes(tool.name)) || [];
								}

								const enabledToolsByConfig = (mcpClient?.tools || []).filter((tool) => config.tools_to_execute?.includes(tool.name)) || [];
								const selectedTools = config.tools_to_execute || [];

								return (
									<TableRow key={`${config.mcp_client_name}-${index}`}>
										<TableCell className="w-[150px]">{config.mcp_client_name}</TableCell>
										<TableCell>
											<MultiSelect
												hideSelectAll
												options={[
													{
														label: "Allow All Tools",
														value: "*",
														description: "Allow all current and future tools",
													},
													...[...availableTools, ...enabledToolsByConfig]
														.filter((tool, index, arr) => arr.findIndex((t) => t.name === tool.name) === index)
														.map((tool) => ({
															label: tool.name,
															value: tool.name,
															description: tool.description,
														})),
												]}
												defaultValue={selectedTools}
												onValueChange={(tools: string[]) => {
													const hadStar = selectedTools.includes("*");
													const hasStar = tools.includes("*");
													if (!hadStar && hasStar) {
														// Just selected "Allow All Tools": set to ["*"] only
														handleUpdateMCPConfig(index, "tools_to_execute", ["*"]);
													} else if (hadStar && hasStar && tools.length > 1) {
														// Had "*", still has "*", but user also selected a specific tool, drop "*"
														handleUpdateMCPConfig(
															index,
															"tools_to_execute",
															tools.filter((t) => t !== "*"),
														);
													} else {
														handleUpdateMCPConfig(index, "tools_to_execute", tools);
													}
												}}
												placeholder={
													selectedTools.length === 0
														? "No tools selected"
														: selectedTools.includes("*")
															? "All tools allowed"
															: "Select tools..."
												}
												variant="inverted"
												className="hover:bg-accent w-full bg-white dark:bg-zinc-800"
												commandClassName="w-full max-w-96"
												modalPopover={true}
												animation={0}
											/>
										</TableCell>
										<TableCell>
											<Button
												type="button"
												variant="ghost"
												size="sm"
												onClick={() => handleRemoveMCPClient(index)}
												data-testid={`vk-delete-mcp-${index}`}
											>
												<Trash2 className="h-4 w-4" />
											</Button>
										</TableCell>
									</TableRow>
								);
							})}
						</TableBody>
					</Table>
				</div>
			)}
		</div>
	);
}