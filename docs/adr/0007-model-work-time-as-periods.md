# Model work time as periods

A Work Calendar stores an ordered list of Critical Work Periods shared by its effective weekdays rather than dedicated start, lunch, and end fields. The first UI may present two periods like the reference design, but treating lunch as an ordinary gap avoids baking one culture-specific break into the scheduler and leaves the domain model able to represent split shifts.
