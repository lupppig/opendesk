package collab

// SidebarSectionIDs are the built-in sidebar sections, in their default order.
var SidebarSectionIDs = []string{"favorites", "inbox", "mail", "teams", "views", "workspace"}

// BadgeValue reports whether v is one of the numbers a sidebar row can show
// (contracts/collab.md "badges").
func BadgeValue(v string) bool { return badgeValues[v] }
