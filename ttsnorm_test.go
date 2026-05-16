package main

import "testing"

func TestNormalizeForTTS(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "date",
			in:   "Purchased on 6/17/2016.",
			want: "Purchased on June 17th, twenty sixteen.",
		},
		{
			name: "date_leading_zeros",
			in:   "Closed 01/05/2003.",
			want: "Closed January 5th, two thousand three.",
		},
		{
			name: "price_no_cents",
			in:   "It cost $260000.",
			want: "It cost 260 thousand dollars.",
		},
		{
			name: "price_with_commas",
			in:   "Value is $725,000.",
			want: "Value is 725 thousand dollars.",
		},
		{
			name: "price_with_cents",
			in:   "Monthly fee $185.48.",
			want: "Monthly fee 185 dollars and 48 cents.",
		},
		{
			name: "price_millions",
			in:   "Worth $1,200,000.",
			want: "Worth 1 million 200 thousand dollars.",
		},
		{
			name: "percent",
			in:   "Rate is 5.25%.",
			want: "Rate is 5.25 percent.",
		},
		{
			name: "zero_percent",
			in:   "Interest 0%.",
			want: "Interest 0 percent.",
		},
		{
			name: "mixed",
			in:   "Bought 6/17/2016 for $260,000 at 3.5%.",
			want: "Bought June 17th, twenty sixteen for 260 thousand dollars at 3.5 percent.",
		},
		{
			name: "two_digit_year_2000s",
			in:   "Closed 1/2/24.",
			want: "Closed January 2nd, twenty twenty four.",
		},
		{
			name: "two_digit_year_1900s",
			in:   "Built 3/15/98.",
			want: "Built March 15th, nineteen ninety eight.",
		},
		{
			name: "no_change",
			in:   "The sky is blue.",
			want: "The sky is blue.",
		},
		{
			name: "year_2000s",
			in:   "Since 1/1/2000.",
			want: "Since January 1st, two thousand.",
		},
		{
			name: "standalone_year_2005",
			in:   "The purchase date was November 18, 2005.",
			want: "The purchase date was November 18, two thousand five.",
		},
		{
			name: "standalone_year_2018",
			in:   "We bought it in 2018.",
			want: "We bought it in twenty eighteen.",
		},
		{
			name: "standalone_year_1998",
			in:   "Built in 1998.",
			want: "Built in nineteen ninety eight.",
		},
		{
			name: "unit_ft_plural",
			in:   "The Eiffel Tower is 330 metres (1,083 ft) tall.",
			want: "The Eiffel Tower is 330 metres (1,083 feet) tall.",
		},
		{
			name: "unit_ft_singular",
			in:   "About 1 ft of snow.",
			want: "About 1 foot of snow.",
		},
		{
			name: "unit_km",
			in:   "It is 5 km away.",
			want: "It is 5 kilometers away.",
		},
		{
			name: "unit_lbs",
			in:   "It weighs 12 lbs.",
			want: "It weighs 12 pounds.",
		},
		{
			name: "unit_mph_not_mi",
			in:   "Up to 60 mph.",
			want: "Up to 60 miles per hour.",
		},
		{
			name: "unit_no_space",
			in:   "A 5km route.",
			want: "A 5 kilometers route.",
		},
		{
			name: "deg_fahrenheit",
			in:   "Water boils at 212°F.",
			want: "Water boils at 212 degrees Fahrenheit.",
		},
		{
			name: "deg_celsius_spaced",
			in:   "Around 100 °C.",
			want: "Around 100 degrees Celsius.",
		},
		{
			name: "deg_bare",
			in:   "Turn it 90°.",
			want: "Turn it 90 degrees.",
		},
		{
			name: "ambiguous_in_untouched",
			in:   "There are 5 in the box.",
			want: "There are 5 in the box.",
		},
		{
			name: "ambiguous_m_untouched",
			in:   "Run 20 m of cable.",
			want: "Run 20 m of cable.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeForTTS(tt.in)
			if got != tt.want {
				t.Errorf("\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}
