using Gtk;
using Adw;
using GLib;
using Json;

public class ToolsView : Gtk.Box {
    private Gtk.Entry base_url_entry;
    private Gtk.Button load_button;
    private Gtk.Stack content_stack;
    private Gtk.Spinner spinner;
    private Gtk.Label error_label;
    private Gtk.Box data_box;

    private HashTable<string, HashTable<string, string>> servers;
    private HashTable<string, GLib.SList<Tool?>> tools;
    private HashTable<string, GLib.SList<Prompt?>> prompts;
    private HashTable<string, GLib.SList<Resource?>> resources;

    private string base_url;

    construct {
        orientation = Gtk.Orientation.VERTICAL;
        spacing = 16;
        servers = new HashTable<string, HashTable<string, string>>(str_hash, str_equal);
        tools = new HashTable<string, GLib.SList<Tool?>>(str_hash, str_equal);
        prompts = new HashTable<string, GLib.SList<Prompt?>>(str_hash, str_equal);
        resources = new HashTable<string, GLib.SList<Resource?>>(str_hash, str_equal);

        setup_ui();
        load_default_url();
    }

    private void setup_ui() {
        // Base URL input
        var url_box = new Gtk.Box(Gtk.Orientation.HORIZONTAL, 6);
        url_box.margin_start = 16;
        url_box.margin_end = 16;
        url_box.margin_top = 16;

        var url_label = new Gtk.Label("Base URL:");
        url_box.append(url_label);

        base_url_entry = new Gtk.Entry();
        base_url_entry.hexpand = true;
        base_url_entry.placeholder_text = "http://localhost:8989";
        url_box.append(base_url_entry);

        load_button = new Gtk.Button.with_label("Load");
        load_button.clicked.connect(() => load_data());
        url_box.append(load_button);

        append(url_box);

        // Content area
        content_stack = new Gtk.Stack();
        content_stack.vexpand = true;

        // Loading spinner
        spinner = new Gtk.Spinner();
        spinner.halign = Gtk.Align.CENTER;
        spinner.valign = Gtk.Align.CENTER;
        content_stack.add_named(spinner, "loading");

        // Error label
        error_label = new Gtk.Label("");
        error_label.halign = Gtk.Align.CENTER;
        error_label.valign = Gtk.Align.CENTER;
        error_label.add_css_class("error");
        content_stack.add_named(error_label, "error");

        // Data view
        var scrolled = new Gtk.ScrolledWindow();
        scrolled.vexpand = true;
        this.data_box = new Gtk.Box(Gtk.Orientation.VERTICAL, 16);
        this.data_box.margin_start = 16;
        this.data_box.margin_end = 16;
        this.data_box.margin_top = 16;
        this.data_box.margin_bottom = 16;

        // Servers section
        var servers_expander = new Gtk.Expander("Servers");
        var servers_list = new Gtk.ListBox();
        servers_expander.child = servers_list;
        this.data_box.append(servers_expander);

        // Tools section
        var tools_expander = new Gtk.Expander("Tools");
        var tools_list = new Gtk.ListBox();
        tools_expander.child = tools_list;
        this.data_box.append(tools_expander);

        // Prompts section
        var prompts_expander = new Gtk.Expander("Prompts");
        var prompts_list = new Gtk.ListBox();
        prompts_expander.child = prompts_list;
        this.data_box.append(prompts_expander);

        // Resources section
        var resources_expander = new Gtk.Expander("Resources");
        var resources_list = new Gtk.ListBox();
        resources_expander.child = resources_list;
        this.data_box.append(resources_expander);

        // Help text when no data
        var help_label = new Gtk.Label("No MCP servers are currently running.\n\nTo get started:\n1. Start mai-wmcp with MCP servers\n2. Example: mai-wmcp \"src/mcps/shell/mai-mcp-shell\"\n3. Or configure your MCP setup in the settings");
        help_label.halign = Gtk.Align.CENTER;
        help_label.valign = Gtk.Align.CENTER;
        help_label.add_css_class("dim-label");
        help_label.wrap = true;
        help_label.xalign = 0.5f;
        this.data_box.append(help_label);

        scrolled.child = this.data_box;
        content_stack.add_named(scrolled, "data");

        append(content_stack);
        content_stack.visible_child_name = "data";
    }

    private void load_default_url() {
        base_url = Environment.get_variable("MAI_TOOL_BASEURL") ?? "http://localhost:8989";
        base_url_entry.text = base_url;
        load_data();
    }

    private void load_data() {
        base_url = base_url_entry.text;
        if (base_url == "") {
            base_url = "http://localhost:8989";
            base_url_entry.text = base_url;
        }

        content_stack.visible_child_name = "loading";
        spinner.start();
        error_label.label = "";

        load_data_async.begin((obj, res) => {
            load_data_async.end(res);
            spinner.stop();
        });
    }

    private async void load_data_async() {
        try {
            // Load servers
            var servers_data = yield run_mai_tool("servers");
            var servers_parser = new Json.Parser();
            servers_parser.load_from_data(servers_data, -1);
            var servers_obj = servers_parser.get_root().get_object();
            servers_obj.foreach_member((obj, server_name, node) => {
                var server_info = new HashTable<string, string>(str_hash, str_equal);
                var info_obj = node.get_object();
                info_obj.foreach_member((info_obj, key, value_node) => {
                    server_info[key] = value_node.get_string();
                });
                servers[server_name] = server_info;
            });

            // Load tools
            var tools_data = yield run_mai_tool("list");
            var tools_parser = new Json.Parser();
            tools_parser.load_from_data(tools_data, -1);
            var tools_obj = tools_parser.get_root().get_object();
            tools_obj.foreach_member((obj, server_name, node) => {
                var tool_list = new GLib.SList<Tool?>();
                var tools_array = node.get_array();
                tools_array.foreach_element((array, index, element) => {
                    var tool_obj = element.get_object();
                    tool_list.append(Tool.from_json(tool_obj));
                });
                tools[server_name] = (owned) tool_list;
            });

            // Load prompts
            var prompts_data = yield run_mai_tool("prompts");
            var prompts_parser = new Json.Parser();
            prompts_parser.load_from_data(prompts_data, -1);
            var prompts_obj = prompts_parser.get_root().get_object();
            prompts_obj.foreach_member((obj, server_name, node) => {
                var prompt_list = new GLib.SList<Prompt?>();
                var prompts_array = node.get_array();
                prompts_array.foreach_element((array, index, element) => {
                    var prompt_obj = element.get_object();
                    prompt_list.append(Prompt.from_json(prompt_obj));
                });
                prompts[server_name] = (owned) prompt_list;
            });

            // Load resources
            var resources_data = yield run_mai_tool("resources");
            var resources_parser = new Json.Parser();
            resources_parser.load_from_data(resources_data, -1);
            var resources_obj = resources_parser.get_root().get_object();
            resources_obj.foreach_member((obj, server_name, node) => {
                var resource_list = new GLib.SList<Resource?>();
                var resources_array = node.get_array();
                resources_array.foreach_element((array, index, element) => {
                    var resource_obj = element.get_object();
                    resource_list.append(Resource.from_json(resource_obj));
                });
                resources[server_name] = (owned) resource_list;
            });

            update_ui();
            content_stack.visible_child_name = "data";

        } catch (Error e) {
            // Check if it's a connection error
            if (e.message.contains("connection refused") || e.message.contains("dial tcp")) {
                error_label.label = "Cannot connect to MCP server at " + base_url + "\n\nMake sure mai-wmcp is running with MCP servers.\nExample: mai-wmcp \"src/mcps/shell/mai-mcp-shell\"";
            } else {
                error_label.label = "Error: " + e.message;
            }
            content_stack.visible_child_name = "error";
        }
    }

    private void update_ui() {
        // Clear existing content and populate with new data
        var data_box = this.data_box;

        // Find the help label (last child)
        Gtk.Widget? help_widget = null;
        var child = data_box.get_last_child();
        while (child != null) {
            if (child is Gtk.Label) {
                help_widget = child;
                break;
            }
            child = child.get_prev_sibling();
        }

        // Check if we have any data
        bool has_data = servers.size() > 0 || tools.size() > 0 || prompts.size() > 0 || resources.size() > 0;
        if (help_widget != null) {
            help_widget.visible = !has_data;
        }

        // Servers section
        var servers_expander = data_box.get_first_child() as Gtk.Expander;
        var servers_list = servers_expander.child as Gtk.ListBox;
        clear_list_box(servers_list);
        foreach (string server_name in servers.get_keys()) {
            var row = new Gtk.ListBoxRow();
            var box = new Gtk.Box(Gtk.Orientation.VERTICAL, 4);
            box.margin_start = 8;
            box.margin_end = 8;
            box.margin_top = 4;
            box.margin_bottom = 4;

            var name_label = new Gtk.Label(server_name);
            name_label.halign = Gtk.Align.START;
            name_label.add_css_class("heading");
            box.append(name_label);

            var server_info = servers[server_name];
            foreach (string key in server_info.get_keys()) {
                var info_label = new Gtk.Label(key + ": " + server_info[key]);
                info_label.halign = Gtk.Align.START;
                info_label.add_css_class("caption");
                box.append(info_label);
            }

            row.child = box;
            servers_list.append(row);
        }
        servers_expander.expanded = servers.size() > 0;
        servers_expander.visible = servers.size() > 0;

        // Tools section
        var tools_expander = data_box.get_first_child().get_next_sibling() as Gtk.Expander;
        var tools_list = tools_expander.child as Gtk.ListBox;
        clear_list_box(tools_list);
        foreach (string server_name in tools.get_keys()) {
            unowned GLib.SList<Tool?> tool_list = tools[server_name];
            if (tool_list.length() == 0) continue;

            var server_row = new Gtk.ListBoxRow();
            var server_box = new Gtk.Box(Gtk.Orientation.VERTICAL, 4);
            server_box.margin_start = 8;
            server_box.margin_end = 8;
            server_box.margin_top = 4;
            server_box.margin_bottom = 4;

            var server_label = new Gtk.Label(server_name);
            server_label.halign = Gtk.Align.START;
            server_label.add_css_class("heading");
            server_box.append(server_label);

            server_row.child = server_box;
            tools_list.append(server_row);

            for (uint i = 0; i < tool_list.length(); i++) {
                var tool = tool_list.nth_data(i);
                var tool_row = new Gtk.ListBoxRow();
                var tool_box = new Gtk.Box(Gtk.Orientation.VERTICAL, 4);
                tool_box.margin_start = 16;
                tool_box.margin_end = 8;
                tool_box.margin_top = 4;
                tool_box.margin_bottom = 4;

                var tool_name_label = new Gtk.Label(tool.name);
                tool_name_label.halign = Gtk.Align.START;
                tool_name_label.add_css_class("body");
                tool_box.append(tool_name_label);

                if (tool.description != null && tool.description != "") {
                    var desc_label = new Gtk.Label(tool.description);
                    desc_label.halign = Gtk.Align.START;
                    desc_label.add_css_class("caption");
                    desc_label.wrap = true;
                    desc_label.xalign = 0;
                    tool_box.append(desc_label);
                }

                tool_row.child = tool_box;
                tools_list.append(tool_row);
            }
        }
        tools_expander.expanded = tools.size() > 0;
        tools_expander.visible = tools.size() > 0;

        // Prompts section
        var prompts_expander = tools_expander.get_next_sibling() as Gtk.Expander;
        var prompts_list = prompts_expander.child as Gtk.ListBox;
        clear_list_box(prompts_list);
        foreach (string server_name in prompts.get_keys()) {
            unowned GLib.SList<Prompt?> prompt_list = prompts[server_name];
            if (prompt_list.length() == 0) continue;

            var server_row = new Gtk.ListBoxRow();
            var server_box = new Gtk.Box(Gtk.Orientation.VERTICAL, 4);
            server_box.margin_start = 8;
            server_box.margin_end = 8;
            server_box.margin_top = 4;
            server_box.margin_bottom = 4;

            var server_label = new Gtk.Label(server_name);
            server_label.halign = Gtk.Align.START;
            server_label.add_css_class("heading");
            server_box.append(server_label);

            server_row.child = server_box;
            prompts_list.append(server_row);

            for (uint i = 0; i < prompt_list.length(); i++) {
                var prompt = prompt_list.nth_data(i);
                var prompt_row = new Gtk.ListBoxRow();
                var prompt_box = new Gtk.Box(Gtk.Orientation.VERTICAL, 4);
                prompt_box.margin_start = 16;
                prompt_box.margin_end = 8;
                prompt_box.margin_top = 4;
                prompt_box.margin_bottom = 4;

                var prompt_name_label = new Gtk.Label(prompt.name);
                prompt_name_label.halign = Gtk.Align.START;
                prompt_name_label.add_css_class("body");
                prompt_box.append(prompt_name_label);

                if (prompt.description != null && prompt.description != "") {
                    var desc_label = new Gtk.Label(prompt.description);
                    desc_label.halign = Gtk.Align.START;
                    desc_label.add_css_class("caption");
                    desc_label.wrap = true;
                    desc_label.xalign = 0;
                    prompt_box.append(desc_label);
                }

                prompt_row.child = prompt_box;
                prompts_list.append(prompt_row);
            }
        }
        prompts_expander.expanded = prompts.size() > 0;
        prompts_expander.visible = prompts.size() > 0;

        // Resources section
        var resources_expander = prompts_expander.get_next_sibling() as Gtk.Expander;
        var resources_list = resources_expander.child as Gtk.ListBox;
        clear_list_box(resources_list);
        foreach (string server_name in resources.get_keys()) {
            unowned GLib.SList<Resource?> resource_list = resources[server_name];
            if (resource_list.length() == 0) continue;

            var server_row = new Gtk.ListBoxRow();
            var server_box = new Gtk.Box(Gtk.Orientation.VERTICAL, 4);
            server_box.margin_start = 8;
            server_box.margin_end = 8;
            server_box.margin_top = 4;
            server_box.margin_bottom = 4;

            var server_label = new Gtk.Label(server_name);
            server_label.halign = Gtk.Align.START;
            server_label.add_css_class("heading");
            server_box.append(server_label);

            server_row.child = server_box;
            resources_list.append(server_row);

            for (uint i = 0; i < resource_list.length(); i++) {
                var resource = resource_list.nth_data(i);
                var resource_row = new Gtk.ListBoxRow();
                var resource_box = new Gtk.Box(Gtk.Orientation.VERTICAL, 4);
                resource_box.margin_start = 16;
                resource_box.margin_end = 8;
                resource_box.margin_top = 4;
                resource_box.margin_bottom = 4;

                var uri_label = new Gtk.Label(resource.uri);
                uri_label.halign = Gtk.Align.START;
                uri_label.add_css_class("body");
                resource_box.append(uri_label);

                if (resource.name != null && resource.name != "") {
                    var name_label = new Gtk.Label(resource.name);
                    name_label.halign = Gtk.Align.START;
                    name_label.add_css_class("caption");
                    resource_box.append(name_label);
                }

                if (resource.description != null && resource.description != "") {
                    var desc_label = new Gtk.Label(resource.description);
                    desc_label.halign = Gtk.Align.START;
                    desc_label.add_css_class("caption");
                    desc_label.wrap = true;
                    desc_label.xalign = 0;
                    resource_box.append(desc_label);
                }

                resource_row.child = resource_box;
                resources_list.append(resource_row);
            }
        }
        resources_expander.expanded = resources.size() > 0;
        resources_expander.visible = resources.size() > 0;
    }

    private void clear_list_box(Gtk.ListBox list_box) {
        var child = list_box.get_first_child();
        while (child != null) {
            var next = child.get_next_sibling();
            list_box.remove(child);
            child = next;
        }
    }

    private async string run_mai_tool(string command) throws Error {
        string[] possible_paths = {
            "/usr/local/bin/mai-tool",
            "/usr/bin/mai-tool",
            "./mai-tool",
            "../tool/mai-tool"
        };

        string? executable_path = null;
        foreach (string path in possible_paths) {
            if (FileUtils.test(path, FileTest.EXISTS)) {
                executable_path = path;
                break;
            }
        }

        if (executable_path == null) {
            // Try which command
            try {
                var which_subprocess = new Subprocess.newv({"which", "mai-tool"},
                    SubprocessFlags.STDOUT_PIPE | SubprocessFlags.STDERR_PIPE);
                Bytes which_output;
                Bytes which_error;
                which_subprocess.communicate(null, null, out which_output, out which_error);
                if (which_subprocess.get_successful()) {
                    executable_path = ((string) which_output.get_data()).strip();
                }
            } catch (Error e) {
                // Ignore
            }
        }

        if (executable_path == null) {
            throw new Error(1, 0, "mai-tool executable not found");
        }

        string[] argv = {executable_path, "-b", base_url, "-j", command};
        var subprocess = new Subprocess.newv(argv, SubprocessFlags.STDOUT_PIPE | SubprocessFlags.STDERR_PIPE);

        Bytes output;
        Bytes error_output;
        subprocess.communicate(null, null, out output, out error_output);

        if (!subprocess.get_successful()) {
            throw new Error(1, 0, "mai-tool command failed");
        }

        return (string) output.get_data();
    }
}