using Gtk;
using Adw;

public class ChatWindow : Gtk.ApplicationWindow {
    private Adw.NavigationSplitView split_view;
    private Gtk.ListBox sidebar;
    private Gtk.Stack content_stack;
    private Gtk.ScrolledWindow messages_scroll;
    private Gtk.ListBox messages_list;
    private Gtk.Entry input_entry;
    private Gtk.Button send_button;
    private SettingsWindow? settings_window;
    private MCPClient mcp_client;
    private bool is_waiting = false;
    private bool mcp_initialized = false;

    public ChatWindow (Gtk.Application app, MCPClient client) {
        Object (application: app, title: "Mai", default_width: 800, default_height: 600);

        mcp_client = client;
        setup_ui ();
    }

    private void setup_ui () {
        // Add close action
        var close_action = new SimpleAction ("close", null);
        close_action.activate.connect (() => {
            this.close ();
        });
        add_action (close_action);

        // Add clear chat action
        var clear_chat_action = new SimpleAction ("clear-chat", null);
        clear_chat_action.activate.connect (() => {
            var child = messages_list.get_first_child ();
            while (child != null) {
                var next = child.get_next_sibling ();
                messages_list.remove (child);
                child = next;
            }
        });
        add_action (clear_chat_action);



        split_view = new Adw.NavigationSplitView ();
        split_view.min_sidebar_width = 0;
                split_view.show_content = true;
        split_view.collapsed = true;

        var header = new Adw.HeaderBar ();
        var toggle_button = new Gtk.ToggleButton ();
        toggle_button.icon_name = "sidebar-show-symbolic";
        toggle_button.tooltip_text = "Toggle sidebar";
        toggle_button.active = false;
        toggle_button.toggled.connect (() => {
            split_view.collapsed = !toggle_button.active;
            if (toggle_button.active) {
                split_view.show_content = true;
            }
        });
        header.pack_start (toggle_button);

        var menu_button = new Gtk.MenuButton ();
        menu_button.icon_name = "open-menu-symbolic";
        var popover = new Gtk.Popover ();
        var box = new Gtk.Box (Gtk.Orientation.VERTICAL, 0);
        var new_window_button = new Gtk.Button.with_label ("New Window");
        new_window_button.clicked.connect (() => {
            this.application.activate_action ("new-window", null);
        });
        box.append (new_window_button);
        var clear_chat_button = new Gtk.Button.with_label ("Clear Chat");
        clear_chat_button.clicked.connect (() => {
            this.activate_action ("clear-chat", null);
        });
        box.append (clear_chat_button);
        var quit_button = new Gtk.Button.with_label ("Quit");
        quit_button.clicked.connect (() => {
            this.application.activate_action ("quit", null);
        });
        box.append (quit_button);
        popover.child = box;
        menu_button.popover = popover;
        header.pack_end (menu_button);

        set_titlebar (header);

        set_child (split_view);

        // Sidebar
        var sidebar_page = new Adw.NavigationPage (new Gtk.Label ("Sidebar"), "Sidebar");
        var sidebar_box = new Gtk.Box (Gtk.Orientation.VERTICAL, 0);
        sidebar = new Gtk.ListBox ();
        var chat_row = new Gtk.ListBoxRow ();
        var chat_row_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
        chat_row_box.margin_start = 8;
        var chat_icon = new Gtk.Image.from_icon_name ("face-smile-symbolic");
        chat_icon.pixel_size = 24;
        chat_row_box.append (chat_icon);
        chat_row_box.append (new Gtk.Label ("Chat"));
        chat_row.child = chat_row_box;
        chat_row.set_size_request (-1, 48); // Minimum height
        sidebar.append (chat_row);

        var console_row = new Gtk.ListBoxRow ();
        var console_row_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
        console_row_box.margin_start = 8;
        var console_icon = new Gtk.Image.from_icon_name ("utilities-terminal-symbolic");
        console_icon.pixel_size = 24;
        console_row_box.append (console_icon);
        console_row_box.append (new Gtk.Label ("Console"));
        console_row.child = console_row_box;
        console_row.set_size_request (-1, 48); // Minimum height
        sidebar.append (console_row);

        var settings_row = new Gtk.ListBoxRow ();
        var settings_row_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
        settings_row_box.margin_start = 8;
        var settings_icon = new Gtk.Image.from_icon_name ("preferences-system");
        settings_icon.pixel_size = 24;
        settings_row_box.append (settings_icon);
        settings_row_box.append (new Gtk.Label ("Settings"));
        settings_row.child = settings_row_box;
        settings_row.set_size_request (-1, 48); // Minimum height
        sidebar.append (settings_row);

        var tools_row = new Gtk.ListBoxRow ();
        var tools_row_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
        tools_row_box.margin_start = 8;
        var tools_icon = new Gtk.Image.from_icon_name ("applications-utilities");
        tools_icon.pixel_size = 24;
        tools_row_box.append (tools_icon);
        tools_row_box.append (new Gtk.Label ("Tools"));
        tools_row.child = tools_row_box;
        tools_row.set_size_request (-1, 48);
        sidebar.append (tools_row);

        var prompts_row = new Gtk.ListBoxRow ();
        var prompts_row_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
        prompts_row_box.margin_start = 8;
        var prompts_icon = new Gtk.Image.from_icon_name ("document-edit");
        prompts_icon.pixel_size = 24;
        prompts_row_box.append (prompts_icon);
        prompts_row_box.append (new Gtk.Label ("Prompts"));
        prompts_row.child = prompts_row_box;
        prompts_row.set_size_request (-1, 48);
        sidebar.append (prompts_row);

        var sessions_row = new Gtk.ListBoxRow ();
        var sessions_row_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
        sessions_row_box.margin_start = 8;
        var sessions_icon = new Gtk.Image.from_icon_name ("user-bookmarks");
        sessions_icon.pixel_size = 24;
        sessions_row_box.append (sessions_icon);
        sessions_row_box.append (new Gtk.Label ("Sessions"));
        sessions_row.child = sessions_row_box;
        sessions_row.set_size_request (-1, 48);
        sidebar.append (sessions_row);

        var context_row = new Gtk.ListBoxRow ();
        var context_row_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
        context_row_box.margin_start = 8;
        var context_icon = new Gtk.Image.from_icon_name ("dialog-information");
        context_icon.pixel_size = 24;
        context_row_box.append (context_icon);
        context_row_box.append (new Gtk.Label ("Context"));
        context_row.child = context_row_box;
        context_row.set_size_request (-1, 48);
        sidebar.append (context_row);

        sidebar.row_selected.connect (on_sidebar_selected);
        sidebar_box.append (sidebar);
        sidebar_page.child = sidebar_box;
        split_view.sidebar = sidebar_page;

        // Content Stack
        var content_page = new Adw.NavigationPage (new Gtk.Label ("Content"), "Content");
        content_stack = new Gtk.Stack ();
        content_page.child = content_stack;
        split_view.content = content_page;

        // Chat View
        var chat_box = new Gtk.Box (Gtk.Orientation.VERTICAL, 0);

        // Messages
        messages_scroll = new Gtk.ScrolledWindow ();
        messages_scroll.vexpand = true;
        messages_scroll.margin_start = 16;
        messages_scroll.margin_end = 16;
        messages_scroll.margin_top = 16;
        messages_scroll.margin_bottom = 16;
        messages_list = new Gtk.ListBox ();
        messages_scroll.child = messages_list;
        chat_box.append (messages_scroll);

        // Input
        var input_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
        input_box.margin_start = 16;
        input_box.margin_end = 16;
        input_box.margin_top = 16;
        input_box.margin_bottom = 16;
        input_entry = new Gtk.Entry ();
        input_entry.hexpand = true;
        input_entry.placeholder_text = "Type your message...";
        input_entry.activate.connect (send_message);
        input_box.append (input_entry);

        send_button = new Gtk.Button.with_label ("Send");
        send_button.clicked.connect (send_message);
        input_box.append (send_button);

        chat_box.append (input_box);

        content_stack.add_named (chat_box, "chat");

        // Console View
        var console_box = new Gtk.Box (Gtk.Orientation.VERTICAL, 0);

        // Console Messages
        var console_messages_scroll = new Gtk.ScrolledWindow ();
        console_messages_scroll.vexpand = true;
        console_messages_scroll.margin_start = 16;
        console_messages_scroll.margin_end = 16;
        console_messages_scroll.margin_top = 16;
        console_messages_scroll.margin_bottom = 16;
        var console_messages_list = new Gtk.ListBox ();
        console_messages_scroll.child = console_messages_list;
        console_box.append (console_messages_scroll);

        // Console Input
        var console_input_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
        console_input_box.margin_start = 16;
        console_input_box.margin_end = 16;
        console_input_box.margin_top = 16;
        console_input_box.margin_bottom = 16;
        var console_input_entry = new Gtk.Entry ();
        console_input_entry.hexpand = true;
        console_input_entry.placeholder_text = "help";
        console_input_entry.activate.connect (() => send_command (console_input_entry, console_messages_list));
        console_input_box.append (console_input_entry);

        var console_send_button = new Gtk.Button.with_label ("Send");
        console_send_button.clicked.connect (() => send_command (console_input_entry, console_messages_list));
        console_input_box.append (console_send_button);

        console_box.append (console_input_box);

        content_stack.add_named (console_box, "console");

        // Settings View - will be created when MCP is ready
        // settings_window = new SettingsWindow (mcp_client);
        // content_stack.add_named (settings_window, "settings");

        // Tools View
        content_stack.add_named (new Gtk.Label ("Tools"), "tools");

        // Prompts View
        content_stack.add_named (new Gtk.Label ("Prompts"), "prompts");

        // Sessions View
        content_stack.add_named (new Gtk.Label ("Sessions"), "sessions");

        // Context View
        content_stack.add_named (new Gtk.Label ("Context"), "context");

        content_stack.visible_child_name = "chat";

        // Initialize MCP and create settings window when ready
        initialize_mcp_and_settings ();

        // Give focus to the input field on startup
        input_entry.grab_focus ();
    }

    private void initialize_mcp_and_settings () {
        // Try to initialize MCP client if not already done
        mcp_client.initialize.begin ((obj, res) => {
            var success = mcp_client.initialize.end (res);
            if (success) {
                mcp_initialized = true;
                // Create settings window now that MCP is ready
                if (settings_window == null) {
                    settings_window = new SettingsWindow (mcp_client);
                    content_stack.add_named (settings_window, "settings");
                }
            }
        });
    }

    private void on_sidebar_selected (Gtk.ListBoxRow? row) {
        if (row == null) return;
        var index = sidebar.get_selected_row ().get_index ();
        if (index == 0) {
            content_stack.visible_child_name = "chat";
        } else if (index == 1) {
            content_stack.visible_child_name = "console";
        } else if (index == 2) {
            // Create settings window if MCP is ready and it doesn't exist
            if (mcp_initialized && settings_window == null) {
                settings_window = new SettingsWindow (mcp_client);
                content_stack.add_named (settings_window, "settings");
            }
            if (settings_window != null) {
                content_stack.visible_child_name = "settings";
            }
        } else if (index == 3) {
            content_stack.visible_child_name = "tools";
        } else if (index == 4) {
            content_stack.visible_child_name = "prompts";
        } else if (index == 5) {
            content_stack.visible_child_name = "sessions";
        } else if (index == 6) {
            content_stack.visible_child_name = "context";
        }
    }



    private void send_message () {
        var text = input_entry.text.strip ();
        if (text == "" || is_waiting) return;

        add_message (text, false);
        input_entry.text = "";
        is_waiting = true;

        var args = new HashTable<string, Value?> (str_hash, str_equal);
        args["message"] = text;
        args["stream"] = "false";

        mcp_client.call_tool.begin ("send_message", args, (obj, res) => {
            var response = mcp_client.call_tool.end (res);
            add_message (response, true);
            is_waiting = false;
        });
    }

    private void send_command (Gtk.Entry entry, Gtk.ListBox messages_list) {
        var text = entry.text.strip ();
        if (text == "" || is_waiting) return;

        add_console_message (text, false, messages_list);
        entry.text = "";
        is_waiting = true;

        var args = new HashTable<string, Value?> (str_hash, str_equal);
        args["command"] = text;

        mcp_client.call_tool.begin ("execute_command", args, (obj, res) => {
            var response = mcp_client.call_tool.end (res);
            add_console_message (response, true, messages_list);
            is_waiting = false;
        });
    }

    private void add_message (string text, bool is_ai) {
        var row = new Gtk.ListBoxRow ();
        if (!is_ai) {
            row.add_css_class ("user-message");
            row.halign = Gtk.Align.END;
        } else {
            row.halign = Gtk.Align.START;
        }
        var box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
        box.margin_start = 8;
        box.margin_end = 8;
        box.margin_top = 4;
        box.margin_bottom = 4;
        var label = new Gtk.Label (text);
        label.wrap = true;
        label.xalign = 0;
        label.selectable = true;
        box.append (label);
        row.child = box;
        messages_list.append (row);
    }

    private void add_console_message (string text, bool is_system, Gtk.ListBox messages_list) {
        var row = new Gtk.ListBoxRow ();
        if (!is_system) {
            row.add_css_class ("console-user-message");
            row.halign = Gtk.Align.START;
        } else {
            row.add_css_class ("console-system-message");
            row.halign = Gtk.Align.START;
        }
        var box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
        box.margin_start = 8;
        box.margin_end = 8;
        box.margin_top = 4;
        box.margin_bottom = 4;
        var label = new Gtk.Label (text);
        label.wrap = true;
        label.xalign = 0;
        label.selectable = true;
        label.add_css_class ("monospace");
        box.append (label);
        row.child = box;
        messages_list.append (row);
    }
}
