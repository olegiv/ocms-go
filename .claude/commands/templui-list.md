List available templUI components or show documentation for a specific component.

Usage: /templui-list
       /templui-list button

Steps:
1. Check if `templui` CLI is installed (`which templui`). If not, install it:
   ```bash
   go install github.com/templui/templui/cmd/templui@latest
   ```

2. Check which components are already installed locally:
   ```bash
   ls internal/ui/ 2>/dev/null
   ```

3. If no specific component name was provided, list all available components:
   ```bash
   templui list
   ```
   Present the output organized by category:
   - **Layout**: Card, Separator, Tabs, Sidebar, Collapsible, AspectRatio
   - **Data Display**: Avatar, Badge, Table, Code, Skeleton, Progress, Rating, Chart
   - **Forms**: Button, Input, Textarea, SelectBox, Checkbox, Radio, Switch, Slider, Label, DatePicker, TimePicker, InputOTP, TagsInput, Form
   - **Feedback**: Alert, Toast, Tooltip, Popover
   - **Navigation**: Breadcrumb, Pagination, Dropdown, Accordion
   - **Overlay**: Dialog, Sheet, Carousel, Calendar
   - **Utility**: CopyButton, Icon, Image/Embed
   Mark already-installed components with a checkmark.

4. If a specific component name was provided:
   - Fetch its documentation from `https://templui.io/docs/components/<name>` using WebFetch
   - Show key props, variants, and usage examples
   - Show the oCMS import path: `"github.com/olegiv/ocms-go/internal/ui/<name>"`
   - Note whether it requires JavaScript (carousel, datepicker, timepicker, chart, code, etc.)

Note: Run `/templui-add <name>` to install a component after looking it up.
